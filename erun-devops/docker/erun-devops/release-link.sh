#!/bin/sh

# release-link surfaces an image-baked release tree in the runtime git folder by
# symlinking the repo dir at the release dir. A tenant <tenant>-devops image
# bakes artifacts (terraform, etc.) into the release dir, which must live OUTSIDE
# /home/erun: the home PVC is mounted over /home/erun and shadows anything baked
# under the home tree, so a symlink whose target is an image-layer path is what
# lets the baked tree show through — and it always reflects the deployed image.
#
# Only a sourceless runtime env (no worktree) should call this; the entrypoint
# gates on that. Here we defend the worktree either way: an empty boot stub is
# replaced, but a populated directory (a mounted or cloned worktree) is left
# untouched so live source is never clobbered.

set -eu

repo_dir="${1:-}"
release_dir="${2:-}"

if [ -z "${repo_dir}" ] || [ -z "${release_dir}" ]; then
    echo "usage: release-link <repo-dir> <release-dir>" >&2
    exit 2
fi

# Nothing baked at the release dir → nothing to surface; leave the git folder be.
[ -d "${release_dir}" ] || exit 0

if [ -L "${repo_dir}" ]; then
    # Already a symlink (ours, or stale from a prior image) — relink to the
    # current release dir. ln -sfn replaces the link without following it.
    ln -sfn "${release_dir}" "${repo_dir}"
    exit 0
fi

if [ -e "${repo_dir}" ]; then
    # A real path exists. Replace only an empty directory (the boot stub); a
    # populated directory is a worktree and must stay.
    if [ -d "${repo_dir}" ] && [ -z "$(ls -A "${repo_dir}" 2>/dev/null)" ]; then
        rmdir "${repo_dir}" 2>/dev/null || exit 0
    else
        exit 0
    fi
fi

mkdir -p "$(dirname "${repo_dir}")"
ln -sfn "${release_dir}" "${repo_dir}"
