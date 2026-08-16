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
# LibreSSL, whose PKCS#12 output `security import` reads, while a Homebrew
# OpenSSL 3 earlier on PATH writes a container it cannot.
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

# create_local_identity mints the self-signed code-signing certificate and puts
# it in a keychain of erun's own, so nothing has to be added to the login
# keychain and no step can stall on a GUI authorization prompt: the password is
# erun's, so the import, the partition list, and the unlock are all answerable
# without the operator.
create_local_identity() {
	workdir=$(mktemp -d) || workdir=
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
	if "$OPENSSL_BIN" req -x509 -newkey rsa:2048 -nodes -days 3650 \
		-config "$workdir/openssl.cnf" -extensions ext \
		-keyout "$workdir/key.pem" -out "$workdir/cert.pem" >/dev/null 2>&1 &&
		"$OPENSSL_BIN" pkcs12 -export -inkey "$workdir/key.pem" -in "$workdir/cert.pem" \
			-name "$IDENTITY" -out "$workdir/identity.p12" -passout pass: >/dev/null 2>&1 &&
		"$SECURITY_BIN" create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN" >/dev/null 2>&1 &&
		"$SECURITY_BIN" set-keychain-settings "$KEYCHAIN" >/dev/null 2>&1 &&
		"$SECURITY_BIN" unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN" >/dev/null 2>&1 &&
		"$SECURITY_BIN" import "$workdir/identity.p12" -k "$KEYCHAIN" -P '' -A \
			-T "$CODESIGN_BIN" >/dev/null 2>&1 &&
		"$SECURITY_BIN" set-key-partition-list -S apple-tool:,apple:,codesign: \
			-s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN" >/dev/null 2>&1; then
		created=1
	fi
	if [ -n "$workdir" ]; then
		rm -rf "$workdir"
	fi
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
