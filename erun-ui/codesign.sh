#!/usr/bin/env sh
#
# Signs locally built desktop artifacts with a stable identity of erun's own.
#
# macOS pins a privacy grant to the identity that signed the code. An ad-hoc
# signature has no identity, so TCC pins the code-directory hash instead — and
# every rebuild mints a new one, which silently drops the grant while System
# Settings still lists the app as allowed. Nothing prompts and nothing errors;
# the capability just starts failing, for the app and for every agent session
# that inherits it as their responsible process. A stable identity is what makes
# a grant outlive a rebuild.
#
# Every path this signs is one the caller already built, so this only ever
# replaces erun's own signature. Off macOS there is no signature to replace and
# no TCC to satisfy, so the whole thing is a no-op that still says so.

set -eu

if [ "$#" -eq 0 ]; then
	printf 'usage: %s <path>...\n' "$0" >&2
	exit 2
fi

# The identity name, keychain file, and keychain password are one contract shared
# with erun-common's host-side signing, which adopts the same identity for the
# macOS binaries it signs as they land; a Go test pins the two ends together.
IDENTITY=${ERUN_CODESIGN_IDENTITY:-ERun Local Development}
KEYCHAIN_FILE=erun-local-signing.keychain-db
# Not a secret. `security create-keychain` demands a password, and this keychain
# holds one self-signed local identity that authenticates nothing to anyone.
KEYCHAIN_PASSWORD=erun-local-signing
# ${HOME:-} so the off-macOS no-op below still holds in an environment that has
# no home at all, rather than failing before it reaches the host check.
KEYCHAIN="${HOME:-}/Library/Keychains/$KEYCHAIN_FILE"

# Absolute defaults, overridable through the same ERUN_<NAME>_BIN seam the Go
# side uses. openssl in particular has to be the system one: macOS ships
# LibreSSL, and a Homebrew OpenSSL 3 earlier on PATH writes a PKCS#12 container
# `security import` cannot read.
CODESIGN_BIN=${ERUN_CODESIGN_BIN:-/usr/bin/codesign}
SECURITY_BIN=${ERUN_SECURITY_BIN:-/usr/bin/security}
OPENSSL_BIN=${ERUN_OPENSSL_BIN:-/usr/bin/openssl}

HOST_OS=${ERUN_HOST_OS_OVERRIDE:-$(uname -s | tr '[:upper:]' '[:lower:]')}

# What an operator loses when the artifact keeps its ad-hoc signature, and the
# one command that recovers a grant once it has stopped applying.
adhoc_consequence() {
	printf '   the artifact keeps its ad-hoc signature, so macOS pins any privacy grant to\n' >&2
	printf '   this build and the next rebuild drops it with no prompt and no error.\n' >&2
	printf '   Recover a grant with: tccutil reset <service> com.sophium.erun\n' >&2
	printf '   (ScreenCapture, Accessibility, ...), then re-grant when prompted.\n' >&2
}

# creation_step runs one step of the identity chain and, when it fails, names the
# step and repeats what the tool said. The chain used to be silenced end to end,
# so the only thing a broken first build could report was that it was broken —
# locating the actual step meant replaying seven commands by hand.
creation_step() {
	step_description=$1
	shift
	if step_output=$("$@" 2>&1); then
		return 0
	fi
	printf '>> code signing: %s failed\n' "$step_description" >&2
	if [ -n "$step_output" ]; then
		printf '%s\n' "$step_output" | sed 's/^/   /' >&2
	fi
	return 1
}

# create_local_identity mints the self-signed code-signing certificate and puts
# it in a keychain of erun's own, so nothing has to be added to the login
# keychain and no step can stall on a GUI authorization prompt: the password is
# erun's, so the import, the partition list, and the unlock are all answerable
# without the operator.
#
# The PKCS#12 carries the same password rather than an empty one. `openssl
# pkcs12 -passout pass:` and `security import -P ''` disagree about how an empty
# PKCS#12 password is encoded, so the import fails MAC verification on a
# container openssl wrote seconds earlier; a non-empty password both ends agree
# on is what makes the identity land. It is the keychain password erun already
# owns, so this is no new secret.
create_local_identity() {
	if ! workdir=$(mktemp -d 2>&1); then
		printf '>> code signing: preparing a working directory failed\n' >&2
		printf '%s\n' "$workdir" | sed 's/^/   /' >&2
		return 1
	fi
	# A config file rather than `openssl req -addext`: LibreSSL's support for
	# that flag is not something a first build on a stock macOS can depend on.
	cat > "$workdir/openssl.cnf" <<EOF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $IDENTITY
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
subjectKeyIdentifier = hash
EOF
	created=0
	if creation_step 'generating the certificate' \
		"$OPENSSL_BIN" req -x509 -newkey rsa:2048 -nodes -days 3650 \
		-config "$workdir/openssl.cnf" -extensions ext \
		-keyout "$workdir/key.pem" -out "$workdir/cert.pem" &&
		creation_step 'packaging the certificate and key' \
			"$OPENSSL_BIN" pkcs12 -export -inkey "$workdir/key.pem" -in "$workdir/cert.pem" \
			-name "$IDENTITY" -out "$workdir/identity.p12" -passout "pass:$KEYCHAIN_PASSWORD" &&
		creation_step 'creating the keychain' \
			"$SECURITY_BIN" create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN" &&
		creation_step 'settling the keychain lock behaviour' \
			"$SECURITY_BIN" set-keychain-settings "$KEYCHAIN" &&
		creation_step 'unlocking the keychain' \
			"$SECURITY_BIN" unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN" &&
		creation_step 'importing the identity into the keychain' \
			"$SECURITY_BIN" import "$workdir/identity.p12" -k "$KEYCHAIN" \
			-P "$KEYCHAIN_PASSWORD" -A -T "$CODESIGN_BIN" &&
		creation_step 'allowing codesign to use the key without a prompt' \
			"$SECURITY_BIN" set-key-partition-list -S apple-tool:,apple:,codesign: \
			-s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN"; then
		created=1
	fi
	rm -rf "$workdir"
	if [ "$created" != 1 ]; then
		# A half-made keychain would be read as holding the identity on the next
		# build, so the failed attempt takes itself back out.
		if [ -e "$KEYCHAIN" ]; then
			"$SECURITY_BIN" delete-keychain "$KEYCHAIN" >/dev/null 2>&1 || true
		fi
		return 1
	fi
	return 0
}

# register_keychain adds erun's keychain to the user search list, because
# `codesign --keychain` does not let an identity be resolved by name — off the
# search list, signing fails with "no identity found" against a keychain that
# demonstrably holds the identity.
#
# The addition is idempotent and is deliberately left in place rather than
# restored: erun-common signs host artifacts by the same identity name long
# after any build has finished, so a search list restored at the end of the
# build would quietly send those back to ad-hoc — the grant loss this exists to
# end. Nothing already on the list is dropped; erun's entry is appended only
# when it is absent, and `security delete-keychain` removes it again.
register_keychain() {
	if ! search_list=$("$SECURITY_BIN" list-keychains -d user 2>&1); then
		printf '>> code signing: reading the keychain search list failed\n' >&2
		printf '%s\n' "$search_list" | sed 's/^/   /' >&2
		return 1
	fi
	set --
	already_listed=0
	# One quoted, indented path per line; splitting on newlines alone keeps a
	# home directory with a space in it intact.
	saved_ifs=$IFS
	IFS='
'
	for listed in $search_list; do
		listed=$(printf '%s' "$listed" | sed -e 's/^[[:space:]]*"*//' -e 's/"*[[:space:]]*$//')
		if [ -n "$listed" ]; then
			if [ "$listed" = "$KEYCHAIN" ]; then
				already_listed=1
			fi
			set -- "$@" "$listed"
		fi
	done
	IFS=$saved_ifs
	if [ "$already_listed" = 1 ]; then
		return 0
	fi
	if ! output=$("$SECURITY_BIN" list-keychains -d user -s "$@" "$KEYCHAIN" 2>&1); then
		printf '>> code signing: adding the keychain to the search list failed\n' >&2
		printf '%s\n' "$output" | sed 's/^/   /' >&2
		return 1
	fi
	printf '>> code signing: added %s to the keychain search list\n' "$KEYCHAIN" >&2
	return 0
}

# Nothing captures codesign's output: a self-signed root it cannot chain to is a
# warning worth seeing beside the success line, not one worth swallowing.
sign_artifact() {
	if [ -n "$KEYCHAIN" ]; then
		"$CODESIGN_BIN" --force --sign "$IDENTITY" --keychain "$KEYCHAIN" "$1"
	else
		"$CODESIGN_BIN" --force --sign "$IDENTITY" "$1"
	fi
}

if [ "$HOST_OS" != "darwin" ]; then
	printf '>> code signing: skipped, %s is not macOS\n' "$HOST_OS" >&2
	exit 0
fi

if [ "${ERUN_SKIP_CODESIGN:-0}" = "1" ]; then
	printf '>> code signing: skipped, ERUN_SKIP_CODESIGN=1\n' >&2
	adhoc_consequence
	exit 0
fi

if [ -n "${ERUN_CODESIGN_IDENTITY:-}" ]; then
	# An operator-supplied identity (a real Developer ID, say) already lives in a
	# keychain the search list reaches, and is never erun's to create or replace.
	KEYCHAIN=
	printf '>> code signing: using identity %s from the keychain search list\n' "$IDENTITY" >&2
elif [ -e "$KEYCHAIN" ]; then
	# The keychain file existing is the whole reuse test. `security find-identity
	# -v` is not usable for it: the valid filter reports zero for a self-signed
	# identity even when it is present and signs fine, so a check written against
	# it would re-create the identity on every build — a new signer each time,
	# which is the grant loss this exists to end.
	printf '>> code signing: using identity %s (%s)\n' "$IDENTITY" "$KEYCHAIN" >&2
else
	printf '>> code signing: creating identity %s (%s)\n' "$IDENTITY" "$KEYCHAIN" >&2
	if ! create_local_identity; then
		printf '>> code signing: could not create identity %s\n' "$IDENTITY" >&2
		adhoc_consequence
		exit 1
	fi
fi

if [ -n "$KEYCHAIN" ]; then
	# Reconciled on every build, not only on the one that created the keychain:
	# anything else that rewrites the search list drops erun's entry, and the
	# next build would then fail to resolve an identity that is right there.
	register_keychain || true
	# The keychain relocks across a reboot, and a locked one makes every signature
	# below fail. codesign reports the consequence if this did not help.
	"$SECURITY_BIN" unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN" >/dev/null 2>&1 || true
fi

status=0
for artifact in "$@"; do
	if sign_artifact "$artifact"; then
		printf '>> code signing: signed %s as %s\n' "$artifact" "$IDENTITY" >&2
	else
		printf '>> code signing: failed to sign %s as %s\n' "$artifact" "$IDENTITY" >&2
		adhoc_consequence
		status=1
	fi
done

exit "$status"
