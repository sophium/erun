---
name: erun-merge
description: Take the current branch from "the work is done" to a review sitting at READY on the erun platform — resolve or accept a target branch, merge it in, commit and push, open or reuse the review, build and record the result. Stops at READY/FAILED and never advances the merge queue. Needs a machine with a configured erun platform cloud alias; an agent environment has none and cannot obtain one, so there it stops after the push and hands the review rungs to a credentialed host. Use when the user says "merge this branch", "land this change", "merge onto main", "advance the merge queue for this branch", "run erun-merge", or any similar request to take a finished change to review.
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

Two questions, both answered before touching git: is there an `erun` binary,
and can it reach the platform these reviews live on? The rungs below do not
all run in the same place — the build does not need platform credentials, and
every `erun review` call does — so answering the second question up front is
what decides whether this skill can run here at all.

```sh
command -v erun >/dev/null 2>&1 || {
  echo "erun is not on PATH. On a laptop: install erun, then 'erun cloud init erun --api-url <url>' and 'erun cloud login --alias <alias>' to connect to the platform this branch's reviews live on."
  exit 1
}

# Resolve the platform alias without reaching the network: --dry-run resolves
# the alias and builds the client, then stops before any HTTP call. erun exits
# 127 when it cannot resolve a usable erun-type alias — its own documented code
# for "this machine cannot make platform calls at all", readable from the exit
# status alone rather than from error text a wrapper might swallow.
probe=0
erun review list --dry-run >/dev/null 2>&1 || probe=$?
if [ "${probe}" -eq 127 ]; then
  cat >&2 <<'EOF'
This machine cannot make erun platform calls, so /erun-merge cannot finish
here: no usable erun platform cloud provider alias is configured.

Do not try to acquire one. `erun cloud init` succeeds unattended, but
`erun cloud login` does not — both of its flows (Device Authorization Grant
and Authorization Code + PKCE) need a human at a browser. No retry, no
timeout, and no piped answer completes one from an unattended environment.

Split the run by what each side can actually do:

  * THIS environment can build. `erun build` needs no platform alias: with
    none configured it simply skips reporting its outcome to the platform.
  * A CREDENTIALED HOST makes every `erun review` call — the already-merged
    check in rung 3, `erun review create` in rung 4, `erun review
    record-build` in rung 5, and everything `erun-merge-queue-drive` runs.

So stop here and hand the branch over. Commit and push it if it is not pushed
yet — that needs only git, and this rung stopped before the merge rungs, so do
not start those either. Then tell the operator that the review steps must run
on a machine with `erun cloud login` already done: either run /erun-merge there
from the top — every rung checks the state it would produce before acting, so
an already-pushed branch resumes rather than repeating work — or complete the
review steps by hand there. The pushed branch is what this environment can
deliver; opening and building the review belongs to the credentialed side.
EOF
  exit 127
fi
```

Do this before touching git. `erun` exists inside a deployed env by
construction; on a laptop it may not, and there is no partial version of this
skill to fall back to — merging without opening a review is not this skill's
job half-done, it is a different, smaller thing.

The second check is a real check, not a warning: it is the CLI's own exit-code
contract (`erun-docs/docs/cli/review.md` § Error behaviour documents 127 as
"could not resolve a usable platform alias"), so an agent cannot read past it
as advice. Any other nonzero probe result is left alone deliberately — a
failure that is not specifically "no platform access" belongs to the real call
that reports it, not to this rung. Stopping here is the whole point: an
environment that walks past this and starts improvising login flows spends its
run on something no unattended agent can complete.

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
if erun build --output json > /tmp/erun-merge-build.json; then
  version=$(jq -r .version /tmp/erun-merge-build.json)
  erun review record-build "${review_id}" --commit "${commit}" --version "${version}"
else
  # A plain build mints its version from a snapshot timestamp, so the failed
  # run's own JSON was never written (the command errors before printing a
  # result) and cannot be re-read. --dry-run mints a valid, same-form version
  # from the same repo state without building anything — enough for a field
  # nothing downstream resolves.
  version=$(erun build --dry-run --output json | jq -r .version)
  erun review record-build "${review_id}" --commit "${commit}" --version "${version}" \
    --failed --failure-detail "erun build failed; see the build log"
fi
```

**`READY` asserts "this commit builds", not "a publishable artifact exists
at version X".** The plain build above mints the version `record-build`
records and builds it; it publishes nothing. The artifact that ships is cut
*after* merge, by the release the accepted review enqueues, which mints its
own version — so nothing consumes a pre-merge `-pr.<sha>` image set, and no
platform path resolves the version string this records. Reaching for
`--release` here instead pays for a two-architecture publish on every pull
request for an artifact with no consumer, and it discards the environment's
`docker.platforms` pin.

**Narrow the platform when the branch predates the environment's pin.** The
build's platform set comes from the *checked-out branch's* `.erun/config.yaml`
(`docker.platforms`, project-wide or per environment), not from the
environment you are running in — so a branch cut before that pin landed still
resolves both architectures and emulates the foreign one, invisibly, at two to
three times the wall clock. A non-release build may narrow it explicitly,
which root `AGENTS.md` § "Release Rules" permits and only `--release` refuses:

```sh
erun build --platform linux/amd64 --output json   # on a machine that only runs amd64
```

Check what the build resolved from its own trace line — `build: platforms
configured as <platforms> (.erun/config.yaml <origin>)` — and pass `--platform`
with the architecture the machine actually runs (`uname -m`) whenever that line
names more than one.

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
- **Trying to acquire platform access it cannot acquire.** When no usable
  alias is configured it stops at this rung's own check and hands the review
  rungs to a credentialed host — never attempting `erun cloud login`, whose
  flows all need a human at a browser, and never guessing an alias or API URL.
- **Fabricating a commit message for uncommitted work it did not write.**
  It asks, rather than inventing one.

## Resuming after a partial failure

Every rung above checks the state that rung would produce before acting, so
re-running `/erun-merge <targetBranch>` after any failure resumes rather than
repeating side effects: an already-merged target is a no-op past the fetch,
an already-pushed branch reports "up to date", an already-open review is
reused instead of re-created, and a fresh build+record always runs last,
because that is the one rung that is safe and meaningful to repeat.
