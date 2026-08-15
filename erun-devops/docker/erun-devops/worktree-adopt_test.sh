#!/bin/sh

# Tests for worktree-adopt.sh: adopt a legacy repository into an empty claim and
# preserve the original, no-op on a populated claim, on an absent legacy tree, on
# a legacy directory that is not a repository, and on a symlinked git folder;
# stay idempotent across boots; retry after an interrupted copy; and reject a
# call with missing arguments.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
adopter="${script_dir}/worktree-adopt.sh"
if [ ! -x "${adopter}" ]; then
    chmod +x "${adopter}"
fi

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t worktree-adopt-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

home_stage="${work_root}/mnt/erun-home"
claim="${work_root}/mnt/erun-worktree"
legacy="${home_stage}/git/petios"

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

reset() {
    rm -rf "${home_stage}" "${claim}"
    mkdir -p "${legacy}" "${claim}"
}

# A tree that looks like the checkout a pre-worktree-volume environment carried:
# a real .git plus tracked and untracked working files.
seed_legacy_repo() {
    mkdir -p "${legacy}/.git/refs" "${legacy}/src"
    printf 'ref: refs/heads/main\n' >"${legacy}/.git/HEAD"
    printf 'package main\n' >"${legacy}/src/main.go"
    printf 'uncommitted\n' >"${legacy}/.env.local"
}

run() {
    "${adopter}" "${legacy}" "${claim}"
}

# --- 1. Empty claim + real repository: adopt, then preserve the original ---
reset
seed_legacy_repo
run
[ -f "${claim}/.git/HEAD" ] || fail "the repository's .git should land on the worktree volume"
[ -f "${claim}/src/main.go" ] || fail "tracked files should land on the worktree volume"
[ -f "${claim}/.env.local" ] || fail "untracked working files should land on the worktree volume"
[ -d "${claim}/.erun-worktree-adopt-partial" ] && fail "the staging dir should be gone once the copy is promoted"
[ -f "${legacy}.pre-worktree-volume/src/main.go" ] || fail "the pre-move copy must be preserved, not deleted"
[ -d "${legacy}" ] || fail "the mount point should be left in place after the original moves aside"

# --- 2. Idempotent: a second boot finds a populated claim and does nothing ---
printf 'edited in pod\n' >"${claim}/src/main.go"
run
[ "$(cat "${claim}/src/main.go")" = "edited in pod" ] ||
    fail "a later boot must not overwrite work done on the worktree volume"

# --- 3. Populated claim + legacy tree: the claim wins, the legacy tree stays ---
reset
seed_legacy_repo
printf 'live\n' >"${claim}/already-here.txt"
run
[ -f "${claim}/already-here.txt" ] || fail "existing claim contents must survive"
[ -e "${claim}/.git" ] && fail "a populated claim must not be overwritten by the legacy tree"
[ -f "${legacy}/.git/HEAD" ] || fail "the legacy tree must stay put when nothing was adopted"
[ -e "${legacy}.pre-worktree-volume" ] && fail "nothing should be set aside when nothing was adopted"

# --- 4. A genuinely new environment: no legacy tree, claim stays empty ---
reset
rm -rf "${legacy}"
run
[ -z "$(ls -A "${claim}")" ] || fail "a new environment's worktree volume should stay empty"

# --- 5. Legacy path exists but is not a repository: leave both alone ---
reset
printf 'stray\n' >"${legacy}/notes.txt"
run
[ -z "$(ls -A "${claim}")" ] || fail "a non-repository must not be adopted"
[ -f "${legacy}/notes.txt" ] || fail "a non-repository must be left where it is"

# --- 6. A symlinked git folder (a sourceless runtime env's baked release) ---
reset
rm -rf "${legacy}"
mkdir -p "${work_root}/opt-release"
printf 'baked\n' >"${work_root}/opt-release/main.tf"
mkdir -p "${work_root}/opt-release/.git"
ln -sfn "${work_root}/opt-release" "${legacy}"
run
[ -z "$(ls -A "${claim}")" ] || fail "a symlinked release tree must not be adopted onto the worktree volume"
[ -L "${legacy}" ] || fail "the symlink must be left intact"

# --- 7. An interrupted copy retries instead of reading as a populated claim ---
reset
seed_legacy_repo
mkdir -p "${claim}/.erun-worktree-adopt-partial/src"
printf 'half\n' >"${claim}/.erun-worktree-adopt-partial/src/main.go"
run
[ -f "${claim}/.git/HEAD" ] || fail "an abandoned staging dir must not block a retry"
[ -d "${claim}/.erun-worktree-adopt-partial" ] && fail "the retry should clear the abandoned staging dir"

# --- 8. A provisioned filesystem's lost+found does not count as content ---
reset
seed_legacy_repo
mkdir -p "${claim}/lost+found"
run
[ -f "${claim}/.git/HEAD" ] || fail "lost+found must not be mistaken for an operator's tree"

# --- 9. Missing arguments are a usage error, not a silent no-op ---
status=0
"${adopter}" >/dev/null 2>&1 || status=$?
[ "${status}" = "2" ] || fail "expected usage exit 2 with no arguments, got ${status}"

echo "PASS: worktree-adopt.sh"
