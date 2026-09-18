---
name: erun-merge-queue-drive
description: Drive one or more reviews already promoted to MERGE through the merge-queue gate — batch their sources into one prospective merge with `erun exec gate-merge` (skipping, per branch, any that conflict), gate the landed stack with one real `erun build`, and push and report MERGED only for branches that actually landed and passed. Reports each actual outcome, including reviews left at MERGE after an inconclusive gate, and never advances, overrides, or promotes the queue itself. Use when the user says "drive the merge queue", "batch these reviews through the gate", "run the merge gate", "gate this promoted review", "build and push the merge queue head", or any similar request to execute the gate for one or more reviews that are already at MERGE.
---

# Drive already-promoted reviews through the gate

Operate only on supplied reviews already at MERGE. Do not promote, advance,
override, resolve skipped conflicts, trigger releases, or change repository
protection as part of this invocation. Queue promotion and ruleset administration
are separate authorized operations.

No automatic drainer invokes this skill today. Gate execution is implemented;
automatic queue driving is not. Use the installed erun commands and their help
for exact flags, and read the target repository's applicable AGENTS.md.

## Preconditions and batch boundary

- Require erun, platform authentication, git access, and the build toolchain.
  Report missing setup from the actual command's refusal; do not invent credentials.
- Resolve each review fresh: status, source, target, name, and remote source SHA.
  Drop non-MERGE reviews without changing their state. Refuse mixed target branches
  and missing source refs.
- The normal API allows one MERGE review per tenant/target. Multi-source
  `gate-merge` support does **not** prove arbitrary multi-review acceptance.
  Do not promote extra reviews or fabricate successful per-review builds to
  bypass this boundary; API batch-verification design remains unresolved.
- Batch membership, landing order, selective retry/bisection, and failure ownership
  are caller policy. Git composition and known infrastructure classification are
  shared erun mechanisms, not shell implementations to duplicate.

## 0. Claim the environment

Before any checkout or gate dispatch, take an exclusive activity lease explicitly
scoped to `environment`, naming this drive and its owner. The default scope is
worktree and is insufficient for a heavy gate.

For an in-pod drive, the command shape is:

```sh
erun activity lease take --tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
  --name "merge-queue drive" --id "$drive_lease" --exclusive --scope environment --ttl 45m \
  --orchestrator "${ERUN_ORCHESTRATOR_ID:-}"
```

Resolve the actual environment explicitly outside a pod; do not silently omit
exclusivity when pod variables are absent. Use a unique drive ID, retain it
through all rungs, renew before expiry, and release on **every** exit path.
A second clone in the same pod is not isolation. On refusal, name the holder and
stop this drive; do not clear another live lease or schedule probes beside it.

## 1. Compose the prospective merge

- Confirm the claimed worktree and each source SHA before mutation.
- Call `erun exec gate-merge` once with repeated `--source`, one `--target`,
  `--under-lease <drive-id>`, and JSON output. Pass each review's name through
  the command's NUL-separated stdin message contract, in source order.
- Capture stdout and stderr separately and preserve the command's own exit status.
  Use a private attempt directory rather than shared fixed /tmp filenames.
- Read `landed` and `skipped` from the result. The shared command fetches once,
  squashes in order, and restores its controlled clean baseline after a conflict;
  do not hand-roll its reset/merge loop or resolve a skipped branch here.
- An empty/all-skipped result is **not** a green build. Stop before building or
  pushing. Report real conflicts against the observed source commit; classify
  fetch/access/infrastructure failures separately instead of failing every review
  merely because stdout was empty.
- Preserve composition JSON, source SHAs, landing commits, order, and skip reasons
  as the attempt artifact. One batch attempt has one gate-run record, not N
  identical rows pretending it ran N times. Per-review GATE builds are distinct.
- A conflict with previously squash-merged dependency content needs deliberate
  branch-history review by its owner; patch-equivalence guesses cannot reliably
  detect every multi-commit squash relationship.

## 2. Gate the landed stack

Renew the environment claim, start one `erun exec gate-run start` for the
target/final merge commit, and run one real `erun build` — never release.
The gate publishes nothing. Save its complete stdout/stderr and composition
under the attempt's durable log/artifact location.

Use a tracked job and explicit execution timeout when the build outlives one
command. Use bounded job awaits and read its own terminal state. Wrapper 124,
Make exit 2, or “still running” is a wait result, not a build verdict.
Keep the environment claim while recovering the actual result.

- For a real failure, report the gate-run first with failing step and readable
  log reference. Use the returned status from erun's shared classifier, not
  caller-side signature matching.
- Known infrastructure failures become INCONCLUSIVE. Leave affected reviews at
  MERGE; do not record failed GATE builds for a non-verdict. A genuinely unknown
  result is also inconclusive after safe recovery checks are exhausted.
- A real unmatched gate failure can record failed GATE builds against actual
  observed commits with useful failure detail. Never fabricate a commit/version.
- Capture stderr: failed builds may emit no JSON body. Preserve upstream status
  across pipelines and diagnostic tails so successful logging cannot hide failure.
- Do not retry infrastructure/push failures blindly. Record cause and required
  recovery; a subsequent authorized attempt gets its own gate-run.

## 3. Record successful evidence

After the real build passes, record the eligible review's successful GATE build
against its exact landing commit, without a release version. For a legitimately
supported batch, retain source-to-review/build/commit mapping and landing order;
do not infer that the final-tip build automatically verifies every source alone.

If recording is refused, preserve the refusal and report the still-running
gate-run INCONCLUSIVE, not a fabricated FAILED build or abandoned RUNNING row.
Otherwise report the batch gate PASSED once. Later push/report failures are
separate anomalies; they do not erase an immutable gate verdict.

Desktop Playwright is part of the real check gate now, so no substitute desktop
attestation is needed. This does not prove native OS behavior outside that suite;
see `erun-ui/AGENTS.md`.

## 4. Push and report acceptance

- Verify the tree/HEAD still matches the gated composition, then push the target
  once through `erun exec push`. Do not force, blindly retry non-fast-forward,
  or publish after an inconclusive/failed gate.
- Only after the push lands, call `erun review report-merged` with the actual
  successful build ID and remote URL. Stop at the first refusal.
- The platform verifies review/build identity, target reachability, and ancestry
  of the last gated target tip. It does **not** require immediate-parent equality;
  release metadata commits may intervene. Respect a refusal rather than inventing
  a success report.
- For each confirmed MERGED source, use `erun exec close-pr` with the gated
  **source** SHA and actual **landing** SHA. A source with no open PR is a no-op;
  a moved PR head is a refusal, not permission to discard new work.
- Skip PR closure for any review whose acceptance did not succeed. Verify linked
  issue state after closure instead of assuming closing references fired.

## 5. Release the claim and report

Release the same explicit environment scope and drive ID, including on failures.
Report review/source/target, landed/skipped state, gate-run status, build IDs and
commits, push/acceptance results, PR closure, logs, and unresolved anomalies.
A review may remain MERGE after an inconclusive result: do not summarize every
outcome as MERGED or FAILED.

Stop here. No release, next-queue promotion, or destructive worktree cleanup is
part of this skill's normal run.

## Resuming after partial failure

Read current reviews, remote refs, saved composition, and gate/build records first.
Before a successful build, a new authorized attempt can recompose and gate the
remaining eligible sources; FAILED reviews need separate re-promotion.

After successful evidence or a partial push/acceptance, do not blindly rerun the
whole skill. Determine what actually landed and resume only missing push/report/
closure steps with the original verified IDs and order. Already accepted reviews
stay accepted. Do not duplicate GATE builds or close a PR whose head moved.

## GitHub ruleset administration (separate authorization)

This is the operational home for the API guide's branch-protection policy, not
an extra rung this skill executes automatically.

- A raw merge-queue push needs a dedicated, narrowly scoped non-human GitHub actor.
  Required PR checks do not identify that push. Decide explicitly whether release
  publication shares the actor or uses its own; GitHub and platform credentials
  are separate trust paths.
- Use `erun exec plan-ruleset-bypass` to prepare a staged migration: first add the
  designated identity alongside existing access, prove its actual gate/push path,
  then demote broad always/exempt bypasses to PR-only access. Do not delete all
  bypass actors, guess numeric role IDs, or create an exempt path invisible to
  the bypass ledger.
- Inspect plans before applying either stage. Read `current_user_can_bypass` with
  each actual credential after application; listing rules or a dry-run git push
  is not equivalent proof. Preserve existing actor entries not intentionally changed.
- Require a status check only after proving its producer reports the actual PR
  head SHA. A prospective squash-commit gate status does not satisfy that check.
- Reconcile bypass rule-suite detail for the specific ruleset against PASSED
  gate-run merge commit/target evidence. Expand push ranges and distinguish tagged
  release commits from gated merges. Flag an unexpected actor even when the pushed
  content was gated. Registry release provenance is not a merge verdict.
- Account provisioning, secret distribution, ruleset writes, and broader-role
  demotion require their own approved scope; this guidance is not authorization.

## Release cadence handoff (separate authorization)

A merge can enqueue a release but no drainer claims/runs it automatically yet.
The proposed coalescing design lives under Release Queue in
`erun-backend/erun-backend-api/AGENTS.md`: newest queued commit, superseding older
rows after roughly 5–10 commits or 30–60 minutes, one running release per tenant,
with expiry/idempotency. These thresholds are provisional, not implemented timers.

When an authorized orchestration task includes releasing, use shared release
orchestration and verify publication before public refs. Bring the gate
environment onto that published version in the same authorized workflow;
release itself does not deploy, and ordinary tenant upgrades remain discretionary.
Do not claim merely enqueueing a row delivered a release or upgrade.
