---
name: erun-merge
description: Take the current branch from "the work is done" to a review sitting at READY on the erun platform — resolve or accept a target branch, merge it in, commit and push, open or reuse the review, build and record the result. Stops at READY/FAILED and never advances the merge queue. Use when the user says "merge this branch", "land this change", "merge onto main", "advance the merge queue for this branch", "run erun-merge", or any similar request to take a finished change to review.
---

# Land the current branch: /erun-merge \<targetBranch\>

Takes the current branch from "the work is done" to **a review sitting at
`READY`** (or `FAILED`, named plainly). `<targetBranch>` is optional — given,
it is the target; omitted, this skill resolves the branch's fork point the
same way the desktop's diff panel does and says which one it picked.

**This skill stops at `READY`. It never advances the merge queue and never
calls the override-advance escape hatch.** `READY` is where the change stops
being yours and becomes the team's — reviewers get assigned, comment, and
leave threads that gate the merge. Advancing the queue
(`erun review queue advance`) is a separate, deliberate decision for whoever
owns that review once its threads are resolved — see
`erun-docs/docs/collaboration/review-loop-topology.md`'s `READY, all
resolved` row. If you are the **builder** role in that loop, this skill is
exactly the `OPEN → READY` row — run it once your change is ready for eyes,
then stop.

For what the merge queue gates on, what happens when its gate fails, and how
an operator unblocks it, see `erun-docs/docs/collaboration/merge-queue.md` —
this skill does not restate that mechanism.

## Before doing anything: can this even run here?

```sh
command -v erun >/dev/null 2>&1 || {
  echo "erun is not on PATH. On a laptop: install erun, then 'erun cloud init erun --api-url <url>' and 'erun cloud login --alias <alias>' to connect to the platform this branch's reviews live on."
  exit 1
}
```

Do this before touching git. `erun` exists inside a deployed env by
construction; on a laptop it may not, and there is no partial version of this
skill to fall back to — merging without opening a review is not this skill's
job half-done, it is a different, smaller thing.

You do not need a separate check for the platform alias: the first platform
call below (`erun review list`) fails cleanly and by itself if none is
configured, naming `erun cloud init erun --api-url <url>` as the fix — see
`erun-docs/docs/cli/review.md`'s error-behaviour table. Stop there and
report the fix; do not guess a URL or an alias.

## The rungs, each skipped when already satisfied

### 1. Resolve the target

```sh
target="${1:-}"
if [ -z "${target}" ]; then
  # --scope all is required: plain `exec diff` (no --scope) never resolves or
  # populates reviewBase at all. The resolved branch can come back either
  # bare ("main") or remote-qualified ("origin/main") depending which
  # candidate won, so strip a leading "origin/" before using it — `exec
  # merge`/`review create` both want the bare branch name and add the remote
  # themselves.
  target=$(erun exec diff --json --scope all | jq -r '.reviewBase.branch // empty')
  target="${target#origin/}"
  if [ -z "${target}" ]; then
    echo "Could not resolve a target branch automatically. Pass one explicitly: /erun-merge <targetBranch>"
    exit 1
  fi
fi
echo "Target branch: ${target}"
```

State the resolved (or given) target before doing anything else — the
operator reads this line to know what "merge" is about to mean.

### 2. Merge the target into the current branch — never rebase

```sh
erun exec merge "${target}"
```

**Merge, never rebase.** Review comments anchor to `commitId` + `filePath` +
`line`; rewriting history orphans every thread on an existing review. This is
exactly what `erun exec merge` does and the only reason it exists.

**On a conflict, stop.** `erun exec merge` reports a conflicted merge as a
distinct outcome, naming every conflicted file, and leaves the worktree
exactly as git left it — mid-merge. Do not attempt to resolve the conflict
yourself, do not guess which side wins. Report the conflicted files and tell
the operator: resolve them and commit, or run `git merge --abort`, then
re-run `/erun-merge`. Re-running after a clean merge (or an abort) starts
this rung over, which is correct — the merge did not happen, so there is
nothing to skip.

If the branch is already even with `${target}` (nothing to merge), the
command is a no-op past the fetch; proceed.

### 3. Commit if dirty, then push

**A push to an already-merged branch succeeds and lands nowhere.** The merge
queue lands a review as a squash commit under a brand-new SHA, so nothing
about a later `git push` to the old branch fails — `gh` and git both report
success, the ref updates, and the commit simply never reaches `main` (this
cost real work once: erun#2007). Check before pushing, not after:

```sh
branch="$(git rev-parse --abbrev-ref HEAD)"
prior_status=$(erun review list --source-branch "${branch}" --output json \
  | jq -r '[.[] | select(.status == "MERGED" or .status == "CLOSED")][0].status // empty')
if [ -n "${prior_status}" ]; then
  echo "${branch} already has a review at ${prior_status}. Pushing more commits here lands nowhere — start a fresh branch from the current target instead of resuming this one."
  exit 1
fi
```

Skip this check for a branch you created earlier in this same run — it cannot
have merged yet. Run it before pushing anything to a branch you are resuming
(picked back up after a pause, handed off from another agent, etc.).

```sh
if [ -n "$(git status --porcelain)" ]; then
  # Ask the operator for a one-line commit message if the tree has
  # uncommitted work; never invent one for changes you did not make.
  echo "<commit message>" | erun exec commit "$(git rev-parse --abbrev-ref HEAD)"
fi
```

A clean tree skips the commit.

**A defect fix names its reproduction before it is pushed.** Root `AGENTS.md`
§ "A Defect Fix Names Its Reproduction" requires a `bug/` branch to name, in a
commit trailer, the test case that reproduces the failure the report
described — or to declare a kind from the closed exemption set. Check it here,
after the commit and before the push, which is the last point an amend is
free:

```sh
node scripts/check-regression-coverage.mjs || exit 1
```

Skip only when the checkout is not this repository (the script lives in
`sophium/erun`). When it fails, it prints the exact trailer block to add:
amend it onto a commit in the range and re-run this rung. Do not push past a
red here and do not rename the branch to dodge it — the whole point is that a
fix ships with the case that would have caught the defect.

```sh
erun exec push "$(git rev-parse --abbrev-ref HEAD)"
```

A push with nothing new is a harmless no-op (git reports "up to date"); this
rung is always safe to re-run.

### 4. Find or create the review

```sh
branch=$(git rev-parse --abbrev-ref HEAD)
existing=$(erun review list --source-branch "${branch}" --target-branch "${target}" --output json \
  | jq -r '[.[] | select(.status != "CLOSED" and .status != "MERGED")][0].reviewId // empty')

if [ -n "${existing}" ]; then
  review_id="${existing}"
  echo "Reusing existing review ${review_id}."
else
  name=$(git log -1 --pretty=%s)
  review_id=$(erun review create --name "${name}" --source-branch "${branch}" --target-branch "${target}" --output json | jq -r .reviewId)
  echo "Opened review ${review_id}: ${name}"
fi
```

Checking first is what makes this rung idempotent — re-running the skill
after a partial failure adopts the review that rung 4 already opened instead
of hitting the branch-pair conflict `erun review create` would otherwise
report. `--name` becomes the eventual squash-merge commit message; the
latest commit subject is a reasonable default, but ask the operator if the
branch carries several unrelated commits and no single subject captures it.

### 5. Build the pushed branch and record it against the review

```sh
commit=$(git rev-parse HEAD)
if erun build --release --output json > /tmp/erun-merge-build.json; then
  version=$(jq -r .version /tmp/erun-merge-build.json)
  erun review record-build "${review_id}" --commit "${commit}" --version "${version}"
else
  # release resolves the version before the per-arch builds run, so it is
  # still knowable even though the failed run's own JSON was never written
  # (the command errors before printing a result). --dry-run recomputes the
  # identical version from the same repo state without touching anything.
  version=$(erun build --release --dry-run --output json | jq -r .version)
  erun review record-build "${review_id}" --commit "${commit}" --version "${version}" \
    --failed --failure-detail "erun build --release failed; see the build log"
fi
```

**Recording the build is the whole transition — there is no separate step
that sets the review's status.** `erun review record-build` is the only way
an erun client moves a review off `OPEN`: a successful build moves it to
`READY` (and on to `MERGE` only if the merge queue already had it at the
head — this skill never triggers that itself); a failed one moves it to
`FAILED`. Do not look for, or improvise, a `review ready`/`review status`
command — it does not exist, deliberately: a `READY` with no build behind it
means something else entirely to the platform (a stalled review being
requeued), so faking one with no build would collide with that meaning.

On a failed build, re-running this skill re-runs the build (it does not
retry on its own) and records a fresh result — nothing here silently retries
a build for you.

### 6. Report and stop

State plainly: the review id, the target branch, the build's outcome
(successful or not, with `commit`/`version`), and the review's resulting
status (`READY` or `FAILED`). Link the review if the platform's web UI has
one. Then stop. Do not run `erun review queue advance`, and do not use
`erun review queue override-advance` under any circumstance — that route
requires a reason recorded against an identity and is an operator's call to
make, never this skill's.

## What this skill refuses outright

- **Rebasing** the current branch onto the target, at any step — always a
  merge commit.
- **Resolving a merge conflict itself.** It stops and names the files;
  resolving is a human (or a separate, deliberate agent action) decision.
- **Advancing the merge queue or overriding its unresolved-thread gate.**
  Both are out of scope by design, not by oversight — see the top of this
  file.
- **Guessing a platform alias or API URL.** If none is configured, it stops
  on the CLI's own error and names the exact setup command.
- **Fabricating a commit message for uncommitted work it did not write.**
  It asks, rather than inventing one.

## Resuming after a partial failure

Every rung above checks the state that rung would produce before acting, so
re-running `/erun-merge <targetBranch>` after any failure resumes rather than
repeating side effects: an already-merged target is a no-op past the fetch,
an already-pushed branch reports "up to date", an already-open review is
reused instead of re-created, and a fresh build+record always runs last,
because that is the one rung that is safe and meaningful to repeat.
