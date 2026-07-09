#!/bin/sh

# Tests for release-link.sh: symlink the repo dir at the release dir on a fresh
# install, replace an empty boot stub, leave a populated worktree untouched,
# relink an existing/stale symlink idempotently, and do nothing when no release
# tree was baked.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
linker="${script_dir}/release-link.sh"
if [ ! -x "${linker}" ]; then
    chmod +x "${linker}"
fi

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t release-link-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

release="${work_root}/release"
repo="${work_root}/git/frs"

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

reset_release() {
    rm -rf "${release}"
    mkdir -p "${release}/terraform-frs/prod"
    printf 'resource "null_resource" "x" {}\n' >"${release}/terraform-frs/prod/main.tf"
}

run() {
    "${linker}" "${repo}" "${release}"
}

# --- 1. Fresh install: absent repo dir becomes a symlink to the release dir ---
reset_release
rm -rf "$(dirname "${repo}")"
run
[ -L "${repo}" ] || fail "expected repo dir to be a symlink after fresh install"
[ "$(readlink "${repo}")" = "${release}" ] || fail "symlink target should be the release dir"
[ -f "${repo}/terraform-frs/prod/main.tf" ] || fail "baked terraform should be visible through the link"

# --- 2. Replace empty stub: an empty real dir (boot stub) is replaced ---
rm -rf "$(dirname "${repo}")"
mkdir -p "${repo}"
run
[ -L "${repo}" ] || fail "expected empty stub dir to be replaced by a symlink"
[ "$(readlink "${repo}")" = "${release}" ] || fail "stub replacement should point at the release dir"

# --- 3. Preserve worktree: a populated real dir is left untouched ---
rm -rf "$(dirname "${repo}")"
mkdir -p "${repo}"
printf 'live edit\n' >"${repo}/local.txt"
run
[ -L "${repo}" ] && fail "a populated worktree must not be turned into a symlink"
[ -f "${repo}/local.txt" ] || fail "populated worktree contents must be preserved"

# --- 4. Idempotent relink: an existing correct symlink stays correct ---
rm -rf "$(dirname "${repo}")"
mkdir -p "$(dirname "${repo}")"
ln -sfn "${release}" "${repo}"
run
[ -L "${repo}" ] || fail "expected the link to remain a symlink"
[ "$(readlink "${repo}")" = "${release}" ] || fail "idempotent relink should keep the release target"

# --- 5. Stale symlink: a link pointing elsewhere is relinked to the release dir ---
rm -rf "$(dirname "${repo}")"
mkdir -p "$(dirname "${repo}")" "${work_root}/stale"
ln -sfn "${work_root}/stale" "${repo}"
run
[ "$(readlink "${repo}")" = "${release}" ] || fail "a stale link should be relinked to the release dir"

# --- 6. No release baked: repo dir is left untouched ---
rm -rf "${release}"
rm -rf "$(dirname "${repo}")"
mkdir -p "${repo}"
run
[ -L "${repo}" ] && fail "no symlink should be created when the release dir is absent"
[ -d "${repo}" ] || fail "repo dir should be left as-is when no release was baked"

echo "PASS: release-link.sh"
