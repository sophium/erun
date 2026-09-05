#!/bin/sh

# worktree-adopt moves an environment's existing repository onto the dedicated
# worktree volume before the pod mounts that volume over it.
#
# An environment created before the worktree claim existed kept its checkout on
# the home volume, at exactly the path the claim now mounts at. Mounting the new
# empty claim there masks the checkout: it is still on the home volume but
# unreachable through erun, and the one-way pod->host workspace sync then
# propagates the empty tree over the operator's mirror. Adopting the tree into
# the claim first is what makes the volume change non-destructive.
#
# Both arguments are staging paths outside /home/erun, so the claim does not
# shadow the tree being read. Adoption is copy-then-set-aside, never move-first:
# a failed run leaves the legacy tree intact, and the claim is only considered
# adopted once the copy landed a real repository. Every later boot finds a
# populated claim and does nothing.

set -eu

legacy_dir="${1:-}"
claim_dir="${2:-}"

if [ -z "${legacy_dir}" ] || [ -z "${claim_dir}" ]; then
    echo "usage: worktree-adopt <legacy-worktree-dir> <worktree-claim-dir>" >&2
    exit 2
fi

# Partial copies land here so an interrupted adoption never presents itself as a
# populated claim on the next boot.
staging_dir="${claim_dir}/.erun-worktree-adopt-partial"

log() {
    echo "worktree-adopt: $1"
}

# The claim counts as empty when it holds nothing an operator put there: a
# freshly provisioned filesystem's lost+found and a previous run's abandoned
# staging dir are both ours to ignore.
claim_is_empty() {
    for entry in "${claim_dir}"/* "${claim_dir}"/.[!.]* "${claim_dir}"/..?*; do
        [ -e "${entry}" ] || continue
        case "${entry##*/}" in
            lost+found | .erun-worktree-adopt-partial) continue ;;
        esac
        return 1
    done
    return 0
}

if [ ! -d "${claim_dir}" ]; then
    log "no worktree volume staged at ${claim_dir}; nothing to adopt"
    exit 0
fi

if ! claim_is_empty; then
    log "worktree volume already holds a tree; leaving it untouched"
    exit 0
fi

if [ ! -d "${legacy_dir}" ] || [ -L "${legacy_dir}" ]; then
    log "no previous tree at ${legacy_dir}; the worktree volume starts empty"
    exit 0
fi

if [ ! -e "${legacy_dir}/.git" ]; then
    log "${legacy_dir} is not a repository; leaving it on the home volume and starting the worktree volume empty"
    exit 0
fi

log "adopting the repository at ${legacy_dir} onto the worktree volume"
rm -rf "${staging_dir}"
mkdir -p "${staging_dir}"
cp -a "${legacy_dir}/." "${staging_dir}/"

if [ ! -e "${staging_dir}/.git" ]; then
    echo "worktree-adopt: copy of ${legacy_dir} did not produce a repository; leaving the legacy tree in place" >&2
    exit 1
fi

for entry in "${staging_dir}"/* "${staging_dir}"/.[!.]* "${staging_dir}"/..?*; do
    [ -e "${entry}" ] || continue
    mv "${entry}" "${claim_dir}/"
done
rmdir "${staging_dir}"

# The adopted copy is proven before the original moves, and the original is set
# aside rather than deleted: losing the tree a second time is the failure this
# script exists to prevent. The sibling name keeps it visible to the operator,
# where the masked-under-a-mount original was not.
preserved_dir="${legacy_dir}.pre-worktree-volume"
if [ -e "${preserved_dir}" ]; then
    log "adopted; leaving the original at ${legacy_dir} because ${preserved_dir} already exists"
    exit 0
fi

mv "${legacy_dir}" "${preserved_dir}"
mkdir -p "${legacy_dir}"
log "adopted; the pre-move copy is preserved at ${preserved_dir}"
