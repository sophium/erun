---
name: erun-merge-queue-drive
description: Drive a review's merge-queue gate to completion once it has already been promoted to MERGE — fetch its target and source, build the prospective squash merge, gate it with a real `erun build` (never --release), and push and report MERGED only on green. Stops at MERGED/FAILED and never advances, overrides, or promotes the queue itself. Use when the user says "drive the merge queue", "run the merge gate", "gate this promoted review", "build and push the merge queue head", or any similar request to execute the gate for a review that is already at MERGE.
---

# Drive a promoted review's gate: /erun-merge-queue-drive \<reviewId\>

Takes a review already sitting at `MERGE` — the head `erun review queue advance`
(or `override-advance`) just promoted — and does the work the platform expects
of "whichever environment gets promoted": fetch the target and source, build
the **prospective squash merge onto the target's current head**, gate it with
a real `erun build`, and push **only on green**. `MERGED` is reached only
after that push actually landed; a report before the push is exactly the
caller's-assertion failure mode `MERGED`'s verification exists to catch (see
`erun-backend/erun-backend-api/AGENTS.md` § "Merge Queue").

**This is not automatic yet.** No poller or watcher promotes a review and then
runs this skill on its own — someone (an operator, or an agent explicitly
asked to) invokes `erun review queue advance`/`override-advance`, then invokes
this skill by name against the review it promoted. Say this plainly if asked
whether the queue "runs itself": today it does not.

**This skill never promotes, advances, or overrides the queue itself.** It
only drives a review that is already `MERGE` — the same division `erun-merge`
draws around `READY`. Promoting the queue head is `erun review queue advance`;
bypassing its unresolved-thread gate is `override-advance`. Both are a
separate, deliberate decision that happens before this skill is ever invoked.

## Before doing anything: can this even run here?

```sh
command -v erun >/dev/null 2>&1 || {
  echo "erun is not on PATH. This skill is for the environment a merge queue promoted — it needs the platform client and a real git remote."
  exit 1
}
```

There is no partial version of this skill. If no erun-type cloud alias is
configured, the first platform call below (`erun review show`) fails cleanly
on its own, naming `erun cloud init erun --api-url <url>` as the fix.

## The rungs, each skipped when already satisfied

### 1. Resolve the review and confirm it is actually `MERGE`

```sh
review_id="${1:?usage: /erun-merge-queue-drive <reviewId>}"
detail=$(erun review show "${review_id}" --output json)
status=$(echo "${detail}" | jq -r .review.status)
target=$(echo "${detail}" | jq -r .review.targetBranch)
source=$(echo "${detail}" | jq -r .review.sourceBranch)
name=$(echo "${detail}" | jq -r .review.name)
if [ "${status}" != "MERGE" ]; then
  echo "Review ${review_id} is ${status}, not MERGE. This skill only drives an already-promoted review — advance the queue first (erun review queue advance --target-branch <branch>), then invoke this skill against the review it promotes."
  exit 1
fi
echo "Driving ${review_id}: ${source} -> ${target} (\"${name}\")"
```

State the resolved review, source, target, and name before doing anything
else. `name` becomes the squash commit message in rung 2 — it is the same
value `review create`'s `--name` set, unchanged by this skill.

### 2. Resolve the source commit before touching anything mutating

```sh
source_commit=$(git ls-remote origin "refs/heads/${source}" | cut -f1)
if [ -z "${source_commit}" ]; then
  echo "refs/heads/${source} does not exist on origin. The review's source branch may have been deleted since it was promoted; report this to whoever owns the queue rather than guessing."
  exit 1
fi
```

This is read-only and needed either way: it is the commit a failed gate in
rung 3 reports against, since a squash-merge failure never produces a
merge commit of its own.

### 3. Build the prospective squash merge

```sh
merge_result=$(echo "${name}" | erun exec gate-merge "${source}" --target "${target}" --output json)
gate_merge_status=$?
```

**On failure** (a fetch error, or a real squash conflict), record a failed
`GATE` build against the source commit and stop:

```sh
if [ "${gate_merge_status}" -ne 0 ]; then
  erun review record-build "${review_id}" --commit "${source_commit}" --gate --failed \
    --failure-detail "erun exec gate-merge failed building the prospective merge onto ${target}: see the gate log"
  echo "Gate-merge failed; recorded a failed GATE build against ${source_commit}. The review should now be FAILED, removed from the queue's front — report the failure to whoever owns ${source} rather than retrying blindly. See erun-docs/docs/collaboration/merge-queue.md if it stays wedged at MERGE instead."
  exit 1
fi
merge_commit=$(echo "${merge_result}" | jq -r .commit)
```

A conflicted squash leaves the working tree mid-conflict — do not resolve it
yourself; the failed-build report above is what unwedges the queue, and the
worktree is cleaned up by whoever re-drives this review after a fix.

### 4. Gate it with a real `erun build` — never `--release`

```sh
if erun build --output json > /tmp/erun-merge-queue-drive-build.json; then
  build_ok=0
else
  build_ok=1
fi
```

The gate is `erun build`, not `erun build --release`: the gate publishes
nothing, and releasing is a separate step triggered off the merge commit only
after `MERGED` is actually reached (see `erun-backend-api/AGENTS.md` § "Merge
Queue" — "The gate is `erun build`, never `erun release`").

**On failure**, record a failed `GATE` build against the merge commit and
stop, the same shape as rung 3's failure path:

```sh
if [ "${build_ok}" -ne 0 ]; then
  erun review record-build "${review_id}" --commit "${merge_commit}" --gate --failed \
    --failure-detail "erun build failed against the prospective merge; see the build log"
  echo "Build failed; recorded a failed GATE build against ${merge_commit}. The review should now be FAILED. The worktree still holds the built-but-rejected merge on a local branch named ${target} — do not push it."
  exit 1
fi
```

### 5. Record the successful GATE build

```sh
build_id=$(erun review record-build "${review_id}" --commit "${merge_commit}" --gate --output json | jq -r .buildId)
```

No `--version`: a `GATE` build carries none, since the gate publishes
nothing. Recording it is what makes rung 7's `report-merged` verifiable —
`review report-merged` refuses with `MERGE_NOT_VERIFIED` if `buildId` does
not name an already-recorded, successful `GATE` build for this exact review.

### 6. Push — only now, because the build was green

```sh
if ! erun exec push "${target}"; then
  echo "Push refused (commonly a non-fast-forward: something moved ${target} while this review held MERGE, which should not happen — see AGENTS.md 'Merge Queue': one merge in flight per (tenant, target_branch)). A GATE build is recorded successful, but MERGED was never reported. Do not retry the push blindly; investigate what moved ${target} first."
  exit 1
fi
```

A push failure here is not a build failure — the queue's own invariant says
nothing else should be able to move `${target}` while this review holds
`MERGE`. Report it as an anomaly, not as routine gate failure noise.

### 7. Report MERGED — verified, not asserted

```sh
remote_url=$(git remote get-url origin)
erun review report-merged "${review_id}" --build-id "${build_id}" --remote-url "${remote_url}"
```

The platform re-checks this rather than trusting it: `buildId` must name the
successful `GATE` build rung 5 just recorded, and fetching `remote_url` must
confirm `merge_commit` is really reachable from `${target}`'s tip with the
parent this review was gated against. A refusal here
(`409 MERGE_NOT_VERIFIED`) means something about rungs 3–6 did not actually
land the way this skill believes — report it plainly rather than retrying.

### 8. Close the pull request GitHub never reconciled with the squash merge

```sh
erun exec close-pr "${source}" --target "${target}" --remote-url "${remote_url}" \
  --gated-commit "${source_commit}" --landing-commit "${merge_commit}"
```

`erun exec gate-merge`'s squash commit is never `${source}`'s branch head, so
GitHub never reconciles a queue merge with its own open pull request on its
own — without this step the review reaches `MERGED` while the GitHub PR list
keeps showing the work as pending forever, and nothing on the PR names
`${merge_commit}` as what actually shipped.

Safe when `${source}` has no open pull request — this is a no-op, not an
error, so a queued plain branch never warns. If it does have one, this
closes it and comments `${merge_commit}` on it. A refusal here means
something pushed to `${source}` after rung 3 fetched it — the review itself
already reached `MERGED`, so report this plainly as a separate anomaly for a
human to reconcile; do not treat it as undoing the merge.

### 9. Report and stop

State plainly: the review id, source and target branches, the gate build's
id and commit, the review's resulting status (`MERGED` or `FAILED`), and
whether the pull request was found and closed. Then stop — this skill does
not trigger a release, does not advance the queue for the next review, and
does not clean up the local `${target}` checkout it left behind.

## What this skill refuses outright

- **Advancing, overriding, or promoting the merge queue.** All three are a
  separate, deliberate decision — see the top of this file.
- **Resolving a squash-merge conflict itself.** It records the failure and
  stops; resolving is a human (or a separate, deliberate agent action) on the
  affected source branch.
- **Reporting `MERGED` before the push actually lands.** Rung 7 only ever
  runs after rung 6 succeeds.
- **Reporting a `GATE` build against a fabricated commit.** A failure before
  rung 3 produces a real commit to report against (rung 2's `source_commit`,
  or rung 4's `merge_commit`) is reported plainly instead, with no build
  record — see the exit in rung 2.
- **Retrying a push failure automatically.** Rung 6's failure is named as an
  anomaly for an operator to look at, not retried.
- **Closing a pull request whose head has moved since the gate ran.** Rung 8
  refuses, loudly, rather than discarding content the gate never saw; the
  review's `MERGED` status stands regardless.

## Resuming after a partial failure

Re-running `/erun-merge-queue-drive <reviewId>` after a stop at rung 3 or 4
is safe: `erun review show` at rung 1 reads the review's current status fresh
each time, so a review already moved to `FAILED` by a previous failed-build
report is caught by rung 1's `MERGE` check rather than silently re-driven —
re-promote it (`erun review queue advance`) after the underlying problem is
fixed, the same recovery `record-build`'s own failure path always required.
A stop after rung 5 but before rung 7 (a push or report failure) is the one
case that needs a human decision rather than a plain re-run: the `GATE` build
already recorded successful is still valid, so re-running rungs 6–7 by hand
against that `build_id` is the right fix once the anomaly rung 6 named is
understood — not re-running the whole skill, which would gate-build and
record a second, redundant `GATE` build. Rung 8 is safe to re-run on its own
after rung 7 already reported `MERGED`: closing an already-closed pull
request is a no-op (its state is no longer `open`, so the lookup finds
nothing), and closing one still open just repeats the same close-and-comment
call.
