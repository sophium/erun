---
name: erun-merge-queue-drive
description: Drive one or more reviews already promoted to MERGE through the merge-queue gate — batch their sources into one prospective merge with `erun exec gate-merge` (skipping, per branch, any that conflict), gate the landed stack with one real `erun build`, and push and report MERGED only for branches that actually landed and passed. Stops at MERGED/FAILED per review and never advances, overrides, or promotes the queue itself. Use when the user says "drive the merge queue", "batch these reviews through the gate", "run the merge gate", "gate this promoted review", "build and push the merge queue head", or any similar request to execute the gate for one or more reviews that are already at MERGE.
---

# Drive one or more promoted reviews' gate: /erun-merge-queue-drive \<reviewId\> [\<reviewId\>...]

Takes one or more reviews already sitting at `MERGE` — the head `erun review
queue advance` (or `override-advance`) just promoted — and does the work the
platform expects of "whichever environment gets promoted": fetch every
target and source, batch them into **one** prospective merge with `erun exec
gate-merge`'s repeatable `--source`, gate the landed stack with a real `erun
build`, and push and report each landed review **MERGED** only on green.
`MERGED` is reached only after that push actually landed; a report before
the push is exactly the caller's-assertion failure mode `MERGED`'s
verification exists to catch (see `erun-backend/erun-backend-api/AGENTS.md`
§ "Merge Queue").

**Batching is what `erun exec gate-merge` is for — do not hand-roll it.**
Looping single-branch gates to pay the build cost once per branch, or
chaining squash merges outside erun's own git plumbing to test whether
unmerged branches compile together, both reproduce a capability erun now
owns directly: pass every branch to batch as its own `--source`, in one
call, and read the per-branch `landed`/`skipped` composition back from the
result — see rung 3.

**This is not automatic yet.** No poller or watcher promotes a review and then
runs this skill on its own — someone (an operator, or an agent explicitly
asked to) invokes `erun review queue advance`/`override-advance`, then invokes
this skill by name against the review(s) it promoted. Say this plainly if
asked whether the queue "runs itself": today it does not.

**This skill never promotes, advances, or overrides the queue itself.** It
only drives reviews that are already `MERGE` — the same division `erun-merge`
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

### 0. Claim the environment for this whole drive

```sh
drive_lease="merge-queue-drive-$$"
if [ -n "${ERUN_TENANT:-}" ] && [ -n "${ERUN_ENVIRONMENT:-}" ]; then
  erun activity lease take --tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
    --name "merge-queue drive $*" --id "$drive_lease" --exclusive --ttl 45m \
    --orchestrator "${ERUN_ORCHESTRATOR_ID:-}" || {
    echo "This environment is held exclusively by other work (named above). A merge-queue drive rewrites the shared worktree and saturates the pod, so it must not run beside anything else — wait for the holder to finish and re-invoke this skill."
    exit 1
  }
fi
```

**Two concurrent drives in one environment corrupt merge accounting, not just
each other's wall-clock.** It has already happened here: one batch reported
`Pushed main to origin (<sha>)` where that sha was the *other* batch's commit,
and two pull requests were closed against work that had not landed. Both
drives rewrite the same worktree, and `git rev-parse HEAD` answers whichever
of them touched it last. The claim closes that window for the whole drive —
`erun exec gate-merge` refuses while another holder has the environment, and
so does every `erun exec job start` here, which is what keeps a probe or gate
job from being scheduled beside the build in rung 4.

Carry `${drive_lease}` through every later rung: pass it to `gate-merge` as
`--under-lease` (rung 3), re-take it to renew before rung 4's long build, and
release it in rung 9. It is a lease, not a lock — 45m without a renewal and it
lapses, so an interrupted drive cannot pin the environment, and the worst a
crashed one costs is a wait.

### 1. Resolve every review and confirm each is actually `MERGE`

```sh
batch=""  # newline-delimited compact JSON, one object per review: {reviewId,source,target,name}
for review_id in "$@"; do
  detail=$(erun review show "${review_id}" --output json)
  status=$(echo "${detail}" | jq -r .review.status)
  if [ "${status}" != "MERGE" ]; then
    echo "Review ${review_id} is ${status}, not MERGE — dropping it from this batch. Advance the queue first (erun review queue advance --target-branch <branch>), then re-invoke this skill against the review it promotes."
    continue
  fi
  entry=$(echo "${detail}" | jq -c --arg id "${review_id}" \
    '{reviewId: $id, source: .review.sourceBranch, target: .review.targetBranch, name: .review.name}')
  echo "Batching ${review_id}: $(echo "${entry}" | jq -r '"\(.source) -> \(.target) (\"\(.name)\")"')"
  batch="${batch}${entry}
"
done
if [ -z "${batch}" ]; then
  echo "No review in this invocation is at MERGE. Nothing to drive."
  exit 1
fi
target=$(echo "${batch}" | jq -rs 'map(.target) | unique | .[0]')
if [ "$(echo "${batch}" | jq -rs 'map(.target) | unique | length')" -ne 1 ]; then
  echo "This batch mixes target branches: $(echo "${batch}" | jq -rs 'map(.target) | unique | join(", ")'). gate-merge builds one working tree onto one --target — split by target branch and drive each as a separate invocation of this skill."
  exit 1
fi
```

State every resolved review, source, target, and name before doing anything
else. `name` becomes that source's own squash commit message in rung 3 — the
same value `review create`'s `--name` set, unchanged by this skill.

**One batch, one target branch.** `gate-merge` builds one working tree onto
one `--target`; a set of reviews that do not all share the same target
branch is not one batch — refused above rather than silently gated against
only one of the target branches.

### 2. Resolve every source commit before touching anything mutating

```sh
batch=$(while IFS= read -r entry; do
  [ -z "${entry}" ] && continue
  source=$(echo "${entry}" | jq -r .source)
  commit=$(git ls-remote origin "refs/heads/${source}" | cut -f1)
  if [ -z "${commit}" ]; then
    echo "refs/heads/${source} does not exist on origin. Its review's source branch may have been deleted since it was promoted; report this to whoever owns the queue rather than guessing." >&2
    exit 1
  fi
  echo "${entry}" | jq -c --arg c "${commit}" '. + {sourceCommit: $c}'
done <<< "${batch}")
subshell_status=$?
[ "${subshell_status}" -ne 0 ] && exit 1
```

This is read-only and needed either way: it is the commit a total gate-merge
failure in rung 3 reports against, since a squash-merge failure never
produces a merge commit of its own, and `gate-merge` itself only reports a
per-source commit for a source it got far enough to attempt.

### 3. Build the prospective merge — batched, with per-branch conflict skip

```sh
source_flags=(); messages=()
while IFS= read -r entry; do
  [ -z "${entry}" ] && continue
  source_flags+=(--source "$(echo "${entry}" | jq -r .source)")
  messages+=("$(echo "${entry}" | jq -r .name)")
done <<< "${batch}"
merge_result=$(printf '%s\0' "${messages[@]}" | head -c -1 | \
  erun exec gate-merge "${source_flags[@]}" --target "${target}" \
  --under-lease "${drive_lease}" --output json \
  2>/tmp/erun-merge-queue-drive-gate-merge.log)
```

`--under-lease` names the claim rung 0 already took, so this drive's own hold
on the environment does not refuse it. Without it, `gate-merge` would refuse
here — it is refused by *any* exclusive holder, including this drive's own.

One `git fetch` covers every branch; one call squashes each `--source` onto
the same working tree in landing order, so the build that follows in rung 4
tests whether they compile **together** — the failure a single-branch gate
cannot see, since a branch can pass alone while breaking against another
unmerged branch.

**A batch where nothing landed is not a build to run — and this is the one
case that matters most to get right; a hand-rolled gate has reported GREEN
and pushed a no-op in exactly this situation before.** `gate-merge` prints
its structured `--output json` result only when at least one source landed
— on total failure (the fetch itself failed) *and* on the "every source
conflicted" refusal alike, it exits non-zero having written nothing to
stdout, so `merge_result` is indistinguishable between the two: empty
either way. Treat both the same — record every review in the batch as a
failed `GATE` build against its own source commit from rung 2, using the
captured stderr (the fetch error, or one `Skipped <remote>/<branch>:
<reason>.` line per source) for the failure detail, and stop before rung 4
ever runs. No build is recorded, and nothing is pushed:

```sh
if [ -z "${merge_result}" ]; then
  reason=$(cat /tmp/erun-merge-queue-drive-gate-merge.log)
  echo "${reason}"
  while IFS= read -r entry; do
    [ -z "${entry}" ] && continue
    review_id=$(echo "${entry}" | jq -r .reviewId)
    source_commit=$(echo "${entry}" | jq -r .sourceCommit)
    erun review record-build "${review_id}" --commit "${source_commit}" --gate --failed \
      --failure-detail "erun exec gate-merge landed nothing for this batch: ${reason}"
    erun exec gate-run start --target-branch "${target}" --review-id "${review_id}" \
      --status failed --failing-step "erun exec gate-merge"
  done <<< "${batch}"
  echo "Nothing landed in this batch; recorded a failed GATE build against every review in it. Stopping here — no erun build runs, nothing is recorded as passing, and nothing is pushed."
  exit 1
fi
landed=$(echo "${merge_result}" | jq -c '.landed[]?')
skipped=$(echo "${merge_result}" | jq -c '.skipped[]?')
```

**A conflicting source is skipped, not fatal when at least one other source
still lands — the rest of the batch keeps going.** `gate-merge` backs a
conflicted squash out with `git reset --hard` and continues against the
clean tree that leaves behind. Record each skip's own failed `GATE` build
and gate-run, then proceed with what did land:

```sh
while IFS= read -r skip; do
  [ -z "${skip}" ] && continue
  source=$(echo "${skip}" | jq -r .sourceBranch)
  reason=$(echo "${skip}" | jq -r .reason)
  review_entry=$(echo "${batch}" | jq -c --arg s "${source}" 'select(.source == $s)')
  review_id=$(echo "${review_entry}" | jq -r .reviewId)
  source_commit=$(echo "${review_entry}" | jq -r .sourceCommit)
  erun review record-build "${review_id}" --commit "${source_commit}" --gate --failed \
    --failure-detail "erun exec gate-merge skipped ${source}: ${reason}"
  erun exec gate-run start --source-branch "${source}" --target-branch "${target}" \
    --source-commit "${source_commit}" --review-id "${review_id}" \
    --status failed --failing-step "git merge --squash"
done <<< "${skipped}"
```

A conflicted squash leaves no shared mid-conflict state to clean up —
`gate-merge` already reset the tree itself; do not attempt to resolve a
skip's conflict here. Whoever owns the skipped source's branch fixes it and
it re-enters a later batch.

**One recurring cause of a squash conflict here is the source branch having
been forked from another PR's branch head before that PR merged** (root
`AGENTS.md` § "Branching Strategy"). The dependency's squash-merge landed
under a new SHA; the source branch still carries the same work under its
original SHAs, so it now conflicts with itself against `${target}`. There is
no cheap, reliable way to detect this automatically before attempting the
squash: the source branch's own commits rarely match the dependency's squash
commit patch-for-patch (a squash of several commits produces one diff that no
individual pre-squash commit's patch equals), so a patch-equivalence check
would miss the common multi-commit case and give false confidence. When a
conflict report names files that look like they belong to a since-merged
dependency, check `git log --oneline ${target}..${source}` for commits that
duplicate content `${target}` already has — that is the fix that unblocked
erun#2007, and it is a human (or a deliberate agent) judgment call, not
something this skill infers for you.

Save `merge_result` to a file (e.g. `/tmp/erun-merge-queue-drive-gate-merge.json`)
— it becomes rung 4's `--log-ref`. `gate_runs` records one row per batch,
not one per review (see below), so this saved composition — `landed`
(branch, source commit, landing commit, in order) and `skipped` (branch,
reason, conflicted files) — is how an operator later answers "which branch
made this batch red."

This skill always drives already-promoted reviews, so `--review-id` is
always set above. A branch gated with no erun review at all (e.g. one gated
by a plain GitHub pull request) calls the same `erun exec gate-run
start`/`report` commands directly, at the same points in this flow, just
omitting `--review-id` and skipping every `erun review ...` call in this
skill entirely for that branch.

### 4. Gate the landed stack with one real `erun build` — never `--release`

```sh
merge_commit=$(echo "${merge_result}" | jq -r .commit)
# Renew the drive's claim before the one step long enough to outlive its TTL.
# Re-taking the same id renews rather than stacking, so this is safe to repeat.
if [ -n "${ERUN_TENANT:-}" ] && [ -n "${ERUN_ENVIRONMENT:-}" ]; then
  erun activity lease take --tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
    --name "merge-queue drive build ${merge_commit}" --id "${drive_lease}" --exclusive --ttl 45m \
    --orchestrator "${ERUN_ORCHESTRATOR_ID:-}"
fi
gate_run_id=$(erun exec gate-run start --target-branch "${target}" \
  --merge-commit "${merge_commit}" --output json | jq -r .gateRunId)
if erun build --output json > /tmp/erun-merge-queue-drive-build.json 2>&1; then
  build_ok=0
else
  build_ok=1
fi
```

One gate-run for the whole landed batch, not one per review — a batch is
structurally one attempt against one working tree with one final tip, so N
rows sharing the same outcome would misrepresent one event as N (see
`erun-backend-api/AGENTS.md` § "Merge Queue"). The gate is `erun build`, not
`erun build --release`: the gate publishes nothing.

**This process must reach the build's real terminal state, not a wrapper's
own timeout.** A long `erun build` run detached into a job (root `AGENTS.md`
§ "Long Gates Detach Themselves Inside An Agent Pod" / "One Agent Job Is One
Run") can outlive one foreground wait; an `await` timing out (exit 124, or a
"still running" report at some elapsed minute count) means exactly that —
still running — never a failure and never a green build. Re-query the job's
actual status instead of reporting either verdict from how long the wait
itself took.

**Capture stderr, not just stdout.** `erun build --output json` only writes
its JSON body on success; on failure it never reaches that write at all, and
the actual reason (`DockerBuildStepError`'s one-line "last words", e.g. a
ghcr.io TLS handshake timeout) goes to stderr. A plain `> file` redirect
therefore captures an **empty file** on the exact failures the classifier
below exists to recognize — `2>&1` above is required, not cosmetic.

**On failure**, classify it before deciding how to report it — a registry or
network failure is not a verdict about the change, and reporting it as one
loses exactly what `erun exec gate-run report`'s own classifier
(`erun-common/gate_run_failure_classifier.go`) already knows how to tell
apart:

```sh
if [ "${build_ok}" -ne 0 ]; then
  failure_reason=$(tail -n 1 /tmp/erun-merge-queue-drive-build.json)
  report=$(erun exec gate-run report "${gate_run_id}" --status failed --failing-step "erun build" \
    --log-ref /tmp/erun-merge-queue-drive-build.json --output json)
  if [ "$(echo "${report}" | jq -r .run.status)" = "INCONCLUSIVE" ]; then
    echo "Build failed on what erun recognizes as a known infrastructure signature (registry or network, not the change) -- reported the gate-run INCONCLUSIVE. Left every landed review at MERGE rather than recording failed GATE builds for them (record-build --gate would itself refuse this failure-detail as the same known signature); re-drive this batch once the registry/network recovers. The worktree still holds the built merge on a local branch named ${target} — do not push it."
  else
    while IFS= read -r landing; do
      [ -z "${landing}" ] && continue
      source=$(echo "${landing}" | jq -r .sourceBranch)
      commit=$(echo "${landing}" | jq -r .commit)
      review_id=$(echo "${batch}" | jq -r --arg s "${source}" 'select(.source == $s) | .reviewId')
      erun review record-build "${review_id}" --commit "${commit}" --gate --failed \
        --failure-detail "${failure_reason}"
    done <<< "${landed}"
    echo "Build failed; recorded a failed GATE build against every landed review's own commit. The worktree still holds the built-but-rejected merge on a local branch named ${target} — do not push it."
  fi
  exit 1
fi
```

Report the `gate-run` first, not `record-build`: its reclassified
`.run.status` is what decides whether this failure is real — do not
pattern-match the log yourself, and do not retry blindly on the suspicion it
was infrastructure. `erun review record-build --gate --failed` applies the
identical classifier to `--failure-detail`
(`ensureNotKnownInfrastructureGateBuildFailure`) and refuses outright when it
matches, precisely so a caller that reordered these two calls cannot record
a false `FAILED` build for a network blip either — but reporting the
gate-run first means this skill never has to depend on that refusal to know
which branch to take.

**If the build's own outcome is still genuinely unknown** after actually
re-querying (an environment fault interrupted this process, not a wrapper
timeout you can just re-check) — report `--status inconclusive` instead of
guessing at `failed` or `passed`; see erun#1931 for why a non-verdict must
never be reported as a red one.

### 5. Record each landed review's successful GATE build, in landing order

```sh
declare -A build_ids  # keyed by source branch, read back in rung 7
while IFS= read -r landing; do
  [ -z "${landing}" ] && continue
  source=$(echo "${landing}" | jq -r .sourceBranch)
  commit=$(echo "${landing}" | jq -r .commit)
  review_id=$(echo "${batch}" | jq -r --arg s "${source}" 'select(.source == $s) | .reviewId')

  git checkout --quiet "${commit}"  # detached, so the diff below is this source's own, not the batch tip's
  desktop_flag=""
  if git diff --name-only "${commit}^" "${commit}" | grep '^erun-ui/' | grep -qv '\.md$'; then
    echo "This landed source (${source}) changes erun-ui/** (desktop). The gate's own erun build does not run erun-ui/playwright (issue #1933), so a green GATE build proves nothing about the desktop frontend by itself."
    echo "Build erun-app and run erun-ui/playwright/run.sh against ${commit} now."
    if <the suite was actually run against ${commit} and passed>; then
      desktop_flag="--desktop-playwright-verified"
    else
      erun exec gate-run report "${gate_run_id}" --status inconclusive \
        --failing-step "erun-ui/playwright not verified against ${commit}"
      echo "Cannot attest desktop coverage here; reported the gate-run INCONCLUSIVE rather than leaving it stuck RUNNING. Hand this build off to someone who can run erun-ui/playwright/run.sh against ${commit}, then re-drive this batch once it passes — do not record a passing GATE build for an unverified desktop change."
      exit 1
    fi
  fi
  if ! build_ids["${source}"]=$(erun review record-build "${review_id}" --commit "${commit}" --gate ${desktop_flag} --output json | jq -r .buildId); then
    erun exec gate-run report "${gate_run_id}" --status inconclusive \
      --failing-step "record-build --gate refused against ${commit}"
    echo "record-build --gate refused for ${review_id}; reported the gate-run INCONCLUSIVE — the erun build itself was green, but this review has no recorded GATE build to show for it. Read the refusal reason before re-driving."
    exit 1
  fi
done <<< "${landed}"
git checkout "${target}"  # back to the branch tip, not detached, before rung 6's push
erun exec gate-run report "${gate_run_id}" --status passed
```

Process landed entries in the order they landed — this is what rung 7 below
depends on. **When the batch landed more than one source, checkout that
source's own commit before diffing it.** `record-build --gate`'s own
desktop-coverage check diffs whatever the working tree's `HEAD` currently is
against its parent — it has no notion of "this review's commit" versus "the
batch's final tip" — so leaving the tree at the final tip while recording an
earlier-landed review would silently check the wrong commit's diff instead.

The `gate-run` outcome is reported here, once, for the whole batch,
immediately once every landed review's build is recorded — independent of
whether the push in rung 6 or the reports in rung 7 below later succeed. A
push or report failure past this point is a separate anomaly (see rung 6),
not something that unmakes this verdict.

No `--version`: a `GATE` build carries none, since the gate publishes
nothing. Recording each review's build is what makes rung 7's
`report-merged` verifiable for it — `review report-merged` refuses with
`MERGE_NOT_VERIFIED` if `buildId` does not name an already-recorded,
successful `GATE` build for that exact review.

**A desktop-coverage or record-build refusal is reported as `INCONCLUSIVE`,
never left `RUNNING` and never reported `FAILED`.** The gate run and a
review's `GATE` build are independent records (`erun-backend-api/AGENTS.md`
§ "Gate Runs") — the `erun build` this attempt already ran genuinely passed,
so `FAILED` would assert a red verdict that never happened, and leaving the
gate run at `RUNNING` forever is exactly the silent-gap failure `erun gate
list` exists to prevent.

### 6. Push once — only now, because the build was green

```sh
git checkout "${target}"
if ! erun exec push "${target}"; then
  echo "Push refused (commonly a non-fast-forward: something moved ${target} while this batch held its reviews at MERGE, which should not happen — see AGENTS.md 'Merge Queue': one merge in flight per (tenant, target_branch)). Every landed review has a GATE build recorded successful, but none is MERGED yet. Do not retry the push blindly; investigate what moved ${target} first."
  exit 1
fi
```

One push lands every landed source at once, since they are one linear stack
on the same local `${target}` branch. A push failure here is not a build
failure — report it as an anomaly, not as routine gate failure noise.

### 7. Report MERGED per landed review — verified, not asserted, one at a time, in landing order

```sh
remote_url=$(git remote get-url origin)
while IFS= read -r landing; do
  [ -z "${landing}" ] && continue
  source=$(echo "${landing}" | jq -r .sourceBranch)
  review_id=$(echo "${batch}" | jq -r --arg s "${source}" 'select(.source == $s) | .reviewId')
  if ! erun review report-merged "${review_id}" --build-id "${build_ids[${source}]}" --remote-url "${remote_url}"; then
    echo "report-merged failed for ${review_id} (${source}); stopping here rather than reporting the rest out of order. Reviews already reported above are MERGED; ${source} onward keep their successful GATE build and resume on a later re-drive."
    break
  fi
done <<< "${landed}"
```

The platform re-checks each report rather than trusting it: `buildId` must
name the successful `GATE` build rung 5 just recorded for that review, and
fetching `remote_url` must confirm that review's own commit is really
reachable from `${target}`'s tip with the parent it was gated against —
the first landed entry's commit parent is the pre-batch tip, and each later
entry's parent is the previous entry's own commit. **Report strictly in
landing order, and stop the loop at the first `report-merged` failure** —
reporting out of order, or past a failed report, breaks that parent chain
for everything after it. Reviews already reported stand as `MERGED`; the
rest keep their successful `GATE` build recorded and resume cleanly on a
later re-drive (see "Resuming" below).

### 8. Close the pull request GitHub never reconciled with the squash merge, per landed review

```sh
while IFS= read -r landing; do
  [ -z "${landing}" ] && continue
  source=$(echo "${landing}" | jq -r .sourceBranch)
  commit=$(echo "${landing}" | jq -r .commit)
  source_commit=$(echo "${batch}" | jq -r --arg s "${source}" 'select(.source == $s) | .sourceCommit')
  erun exec close-pr "${source}" --target "${target}" --remote-url "${remote_url}" \
    --gated-commit "${source_commit}" --landing-commit "${commit}"
done <<< "${landed}"
```

`erun exec gate-merge`'s squash commit is never a source branch's own head,
so GitHub never reconciles a queue merge with its own open pull request on
its own — without this step a review reaches `MERGED` while the GitHub PR
list keeps showing the work as pending forever.

Safe when a source has no open pull request — a no-op, not an error, so a
queued plain branch never warns. Skip any source whose `report-merged` did
not run in rung 7 (rung 7 stopped before it) — closing its pull request
before the review is actually `MERGED` would misreport what shipped. A
refusal here for a landed, reported source means something pushed to it
after rung 3 fetched it — that review's `MERGED` status stands regardless;
report this plainly as a separate anomaly for a human to reconcile.

### 9. Release the environment, then report and stop

```sh
if [ -n "${ERUN_TENANT:-}" ] && [ -n "${ERUN_ENVIRONMENT:-}" ]; then
  erun activity lease release --tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
    --id "${drive_lease}" --exclusive
fi
```

Release it on **every** exit path, including the early ones — rung 0's own
refusal aside, every `exit 1` above should release first, or the next drive
waits out the remaining TTL for no reason. Releasing a claim that already
lapsed succeeds, so this is safe to run unconditionally.

State plainly, for the whole batch: every review's id, source and target
branches, its landed/skipped outcome, its `GATE` build id and commit where
one was recorded, its resulting status (`MERGED` or `FAILED`), and whether
its pull request was found and closed. Then stop — this skill does not
trigger a release, does not advance the queue for the next review, and does
not clean up the local `${target}` checkout it left behind.

## What this skill refuses outright

- **Advancing, overriding, or promoting the merge queue.** All three are a
  separate, deliberate decision — see the top of this file.
- **Resolving a squash-merge conflict itself.** A skipped source's conflict
  is recorded and left for its owner; nothing here resolves it.
- **Reporting `MERGED` before the push actually lands.** Rung 7 only ever
  runs after rung 6 succeeds.
- **Reporting a `GATE` build against a fabricated commit.** A failure before
  a real commit exists reports plainly instead, with no build record.
- **Reporting a passing gate when the batch landed nothing.** An all-skipped
  batch is not a green build; rung 3 stops before rung 4 ever runs, and
  nothing gets pushed or recorded as successful.
- **Treating a driving wrapper's own timeout as the gate's verdict.** Exit
  124, exit 2, or "still running after N minutes" from whatever launched
  `erun build` reports only that the wait ended, never that the build failed
  or passed — re-query the job's actual recorded status.
- **Retrying a push failure, or a build failure erun classified as a known
  infrastructure signature, automatically.** Both are named as anomalies
  (or, for the classifier case, `INCONCLUSIVE`) for an operator to look at,
  not retried blind.
- **Closing a pull request whose head has moved since the gate ran.** Rung 8
  refuses, loudly, rather than discarding content the gate never saw.
- **Running beside another drive, gate, or probe job in the same
  environment.** Rung 0 claims the environment and stops if it cannot get it.
  Two drives sharing one worktree is how a batch came to report pushing
  another batch's commit.

## Resuming after a partial failure

Re-running `/erun-merge-queue-drive <reviewId>...` after a stop at rung 1–4
is safe: rung 1 reads every review's current status fresh each time, so a
review already moved to `FAILED` by a previous failed-build report is caught
by rung 1's `MERGE` check and dropped rather than silently re-driven —
re-promote it after the underlying problem is fixed. A review left at
`MERGE` after an `INCONCLUSIVE` classification (rung 4's known-infrastructure
path) is re-driven the same way once the registry/network recovers. Passing
a smaller batch (dropping a review whose branch needs a fix) is always safe;
passing a wider one just re-attempts the whole stack fresh. Each re-run of
rungs 3–4 starts a new `gate-run`, which is correct rather than redundant:
`erun gate list` should show every attempt, not just the last one.

A stop after rung 5 but before rung 7 finishes (a push or report-merged
failure partway through the batch) needs a human decision rather than a
plain re-run for whichever reviews already reported `MERGED` — their `GATE`
build stands, they are done. For reviews that landed and were recorded
successful but never got a `report-merged` call (because an earlier one in
the same batch failed first), re-running rungs 6–7 by hand — push once more
if needed, then `report-merged` in the same landing order starting from
wherever the previous run stopped — is the right fix, not re-running the
whole skill, which would gate-build and record a second, redundant `GATE`
build for reviews already recorded. Rung 8 is safe to re-run per review on
its own after rung 7 already reported it `MERGED`: closing an already-closed
pull request is a no-op.
