#!/bin/sh

# Tests for skills-install.sh: install when absent, refresh an unmodified skill
# when the baked copy changed, preserve an in-pod edit, and refresh a legacy
# copy that has no provenance marker.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
installer="${script_dir}/skills-install.sh"
if [ ! -x "${installer}" ]; then
    chmod +x "${installer}"
fi

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t skills-install-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

baked="${work_root}/baked"
dest="${work_root}/dest"
skill="erun-build-env"
baked_skill="${baked}/${skill}"
dest_skill="${dest}/${skill}"
marker="${dest_skill}/.erun-skill-baked-sha256"

mkdir -p "${baked_skill}"

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
printf 'v1 body\n' >"${baked_skill}/SKILL.md"
printf 'ref v1\n' >"${baked_skill}/reference.md"
run
assert_file_contains "${dest_skill}/SKILL.md" "v1 body"
assert_file_contains "${dest_skill}/reference.md" "ref v1"
[ -f "${marker}" ] || fail "expected marker after fresh install"

# --- 2. Refresh: an unmodified install tracks a changed baked copy ---
printf 'v2 body\n' >"${baked_skill}/SKILL.md"
printf 'ref v2\n' >"${baked_skill}/reference.md"
run
assert_file_contains "${dest_skill}/SKILL.md" "v2 body"
assert_file_contains "${dest_skill}/reference.md" "ref v2"

# --- 3. Preserve: an in-pod edit is never clobbered ---
printf 'operator edit\n' >"${dest_skill}/SKILL.md"
printf 'v3 body\n' >"${baked_skill}/SKILL.md"
run
assert_file_contains "${dest_skill}/SKILL.md" "operator edit"

# --- 4. Legacy: a copy with no marker is refreshed to baked ---
printf 'ancient body\n' >"${dest_skill}/SKILL.md"
rm -f "${marker}"
printf 'v4 body\n' >"${baked_skill}/SKILL.md"
run
assert_file_contains "${dest_skill}/SKILL.md" "v4 body"
[ -f "${marker}" ] || fail "expected marker after legacy refresh"

# --- 5. Directory with no SKILL.md is skipped, not installed ---
mkdir -p "${baked}/not-a-skill"
printf 'stray\n' >"${baked}/not-a-skill/README.md"
run
[ -e "${dest}/not-a-skill" ] && fail "a baked dir with no SKILL.md must not be installed"

echo "PASS: skills-install.sh"
