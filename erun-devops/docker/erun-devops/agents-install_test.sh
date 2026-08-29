#!/bin/sh

# Tests for agents-install.sh: install when absent, refresh an unmodified
# agent when the baked copy changed, preserve an in-pod edit, and refresh a
# legacy copy that has no provenance marker.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
installer="${script_dir}/agents-install.sh"
if [ ! -x "${installer}" ]; then
    chmod +x "${installer}"
fi

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t agents-install-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

baked="${work_root}/baked"
dest="${work_root}/dest"
agent="erun-builder.md"
baked_agent="${baked}/${agent}"
dest_agent="${dest}/${agent}"
marker="${dest_agent}.erun-agent-baked-sha256"

mkdir -p "${baked}"

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

assert_file_contains() {
    # $1 file, $2 substring
    grep -q -- "$2" "$1" || fail "expected '$2' in $1; got: $(cat "$1" 2>/dev/null)"
}

run() {
    "${installer}" "${baked}" "${dest}"
}

# --- 1. Fresh install: absent destination is populated and marked ---
printf 'v1 body\n' >"${baked_agent}"
run
assert_file_contains "${dest_agent}" "v1 body"
[ -f "${marker}" ] || fail "expected marker after fresh install"

# --- 2. Refresh: an unmodified install tracks a changed baked copy ---
printf 'v2 body\n' >"${baked_agent}"
run
assert_file_contains "${dest_agent}" "v2 body"

# --- 3. Preserve: an in-pod edit is never clobbered ---
printf 'operator edit\n' >"${dest_agent}"
printf 'v3 body\n' >"${baked_agent}"
run
assert_file_contains "${dest_agent}" "operator edit"

# --- 4. Legacy: a copy with no marker is refreshed to baked ---
printf 'ancient body\n' >"${dest_agent}"
rm -f "${marker}"
printf 'v4 body\n' >"${baked_agent}"
run
assert_file_contains "${dest_agent}" "v4 body"
[ -f "${marker}" ] || fail "expected marker after legacy refresh"

# --- 5. A second baked agent installs independently ---
printf 'reviewer body\n' >"${baked}/erun-reviewer.md"
run
assert_file_contains "${dest}/erun-reviewer.md" "reviewer body"

echo "PASS: agents-install.sh"
