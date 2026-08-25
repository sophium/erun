#!/usr/bin/env bash
#
# The pre-merge gate for erun#1290: evaluated on the MERGE RESULT, not the
# branch tip.
#
# The whole point of this script is the accumulation bug class from #1282:
# several PRs each independently green, only red once merged, because a
# check on the branch tip cannot see what the branch tip plus main's current
# state would total. This script builds that merge in a scratch worktree —
# never in the caller's own checkout — and runs the full repo-gate.sh against
# it.
#
# Usage:
#   scripts/merge-gate.sh <base-ref> <head-ref> [--post-status]
#
# <base-ref> is normally "main" (or "origin/main"); <head-ref> is the PR
# branch, a remote ref, or a commit SHA. Both are resolved with `git rev-
# parse` against the caller's own repository, so fetch whatever you need
# first (`git fetch origin main <branch>`).
#
# --post-status posts a GitHub commit status (context "erun/merge-gate") on
# <head-ref>'s resolved SHA via `gh api`, success or failure. This is
# deliberately a plain script calling the same Status API a hosted CI system
# would use — NOT a GitHub Actions workflow. Root AGENTS.md forbids
# reintroducing GitHub Actions for build/test gating (#521: GitHub-hosted
# runners defeat erun's own daemon-centric build caching); this keeps the
# actual test execution on erun's own infrastructure while still giving
# GitHub something to hang branch protection on. Requires `gh` authenticated
# with push access to the repo.
set -uo pipefail

if [ "$#" -lt 2 ]; then
	echo "usage: $0 <base-ref> <head-ref> [--post-status]" >&2
	exit 2
fi

BASE_REF="$1"
HEAD_REF="$2"
POST_STATUS=0
if [ "${3:-}" = "--post-status" ]; then
	POST_STATUS=1
fi

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)"
ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)"
cd "$ROOT"

BASE_SHA="$(git rev-parse "$BASE_REF")" || exit 1
HEAD_SHA="$(git rev-parse "$HEAD_REF")" || exit 1

WORKTREE="$(mktemp -d "${TMPDIR:-/tmp}/erun-merge-gate.XXXXXX")"
BRANCH_NAME="merge-gate/$(date +%s 2>/dev/null || echo scratch)-$$"
cleanup() {
	git -C "$ROOT" worktree remove --force "$WORKTREE" >/dev/null 2>&1 || true
	git -C "$ROOT" branch -D "$BRANCH_NAME" >/dev/null 2>&1 || true
	rm -rf "$WORKTREE"
}
trap cleanup EXIT

echo ">> creating scratch worktree at $WORKTREE from $BASE_REF ($BASE_SHA)"
git worktree add -b "$BRANCH_NAME" "$WORKTREE" "$BASE_SHA" >/dev/null

post_status() {
	[ "$POST_STATUS" = "1" ] || return 0
	local state="$1"
	local description="$2"
	local slug
	slug="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null)" || return 0
	gh api "repos/${slug}/statuses/${HEAD_SHA}" \
		-f state="$state" \
		-f context="erun/merge-gate" \
		-f description="$description" >/dev/null || true
}

echo ">> merging $HEAD_REF ($HEAD_SHA) into $BASE_REF ($BASE_SHA), no-ff, in the scratch worktree"
if ! git -C "$WORKTREE" merge --no-ff --no-edit "$HEAD_SHA"; then
	echo "!! merge conflict between $BASE_REF and $HEAD_REF — cannot evaluate a merge result that doesn't exist" >&2
	git -C "$WORKTREE" merge --abort >/dev/null 2>&1 || true
	post_status "failure" "merge conflict"
	exit 1
fi

echo ">> running scripts/repo-gate.sh against the merge result"
if "$WORKTREE/scripts/repo-gate.sh"; then
	echo ">> merge-gate PASSED: $HEAD_REF merges cleanly into $BASE_REF and the whole-repo gate is green on the result"
	post_status "success" "whole-repo gate green on the merge result"
	exit 0
else
	echo "!! merge-gate FAILED: the merge of $HEAD_REF into $BASE_REF is red, even though the branch tip may be green" >&2
	post_status "failure" "whole-repo gate red on the merge result"
	exit 1
fi
