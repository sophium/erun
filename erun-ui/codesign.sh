#!/usr/bin/env sh
# Signs local desktop artifacts with erun's stable identity: TCC pins an ad-hoc
# signature's hash, so every rebuild would silently drop privacy grants.

set -eu

if [ "$#" -eq 0 ]; then
	printf 'usage: %s <path>...\n' "$0" >&2
	exit 2
fi

# Shared contract with erun-common's host-side signing; a Go test pins both ends.
IDENTITY=${ERUN_CODESIGN_IDENTITY:-ERun Local Development}
KEYCHAIN_FILE=erun-local-signing.keychain-db
# Not a secret: create-keychain demands one, and the identity authenticates nothing.
KEYCHAIN_PASSWORD=erun-local-signing
# ${HOME:-} so the off-macOS no-op survives an unset HOME.
KEYCHAIN="${HOME:-}/Library/Keychains/$KEYCHAIN_FILE"

# openssl must be the system LibreSSL: Homebrew OpenSSL 3 writes PKCS#12 that
# `security import` cannot read.
CODESIGN_BIN=${ERUN_CODESIGN_BIN:-/usr/bin/codesign}
SECURITY_BIN=${ERUN_SECURITY_BIN:-/usr/bin/security}
OPENSSL_BIN=${ERUN_OPENSSL_BIN:-/usr/bin/openssl}

HOST_OS=${ERUN_HOST_OS_OVERRIDE:-$(uname -s | tr '[:upper:]' '[:lower:]')}

adhoc_consequence() {
	printf '   the artifact keeps its ad-hoc signature, so macOS pins any privacy grant to\n' >&2
	printf '   this build and the next rebuild drops it with no prompt and no error.\n' >&2
	printf '   Recover a grant with: tccutil reset <service> com.sophium.erun\n' >&2
	printf '   (ScreenCapture, Accessibility, ...), then re-grant when prompted.\n' >&2
}

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

# A keychain of erun's own, so no step stalls on a GUI authorization prompt.
# The PKCS#12 reuses its password: openssl and security encode an empty one differently.
create_local_identity() {
	if ! workdir=$(mktemp -d 2>&1); then
		printf '>> code signing: preparing a working directory failed\n' >&2
		printf '%s\n' "$workdir" | sed 's/^/   /' >&2
		return 1
	fi
	# A config file, not -addext: stock macOS LibreSSL may lack it.
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
		# A half-made keychain would pass the next build's reuse check.
		if [ -e "$KEYCHAIN" ]; then
			"$SECURITY_BIN" delete-keychain "$KEYCHAIN" >/dev/null 2>&1 || true
		fi
		return 1
	fi
	return 0
}

# `codesign --keychain` cannot resolve an identity by name, so the keychain joins the
# search list. Left there: erun-common signs by the same name after the build.
register_keychain() {
	if ! search_list=$("$SECURITY_BIN" list-keychains -d user 2>&1); then
		printf '>> code signing: reading the keychain search list failed\n' >&2
		printf '%s\n' "$search_list" | sed 's/^/   /' >&2
		return 1
	fi
	set --
	already_listed=0
	# Split on newlines only so a home path with spaces survives.
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

# Output is not captured: a self-signed root warning is worth seeing.
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
	# An operator-supplied identity is never erun's to create or replace.
	KEYCHAIN=
	printf '>> code signing: using identity %s from the keychain search list\n' "$IDENTITY" >&2
elif [ -e "$KEYCHAIN" ]; then
	# Keychain-file presence is the reuse test: `find-identity -v` reports zero for a
	# self-signed identity.
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
	# Every build: anything rewriting the search list drops erun's entry.
	register_keychain || true
	# The keychain relocks across reboots.
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
