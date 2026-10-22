---
name: erun-merge-queue-drive
description: Drive one or more reviews already promoted to MERGE through the merge queue — batch their sources into one prospective merge with `erun exec gate-merge` (skipping, per branch, any that conflict, except one confined to the regenerable `atlas.sum` checksum, which is resolved from the migrations on disk and the branch lands), build the merge commit with one plain `erun build`, record that build against the review with the version it minted, and push and report MERGED only for branches that actually landed on a successful build. Reports each actual outcome, including reviews left at MERGE after an infrastructure non-verdict, and never advances, overrides, or promotes the queue itself. Requires a machine with a configured erun platform cloud alias, since every rung is a platform call; an agent environment can hold one only if `erun init` provisioned it from a signed-in host, so where it has none it stops before claiming the environment and hands the drive to a credentialed host. Use when the user says "drive the merge queue", "batch these reviews through the merge queue", "run the merge gate", "land this promoted review", "build and push the merge queue head", or any similar request to land one or more reviews that are already at MERGE.
---

# Drive already-promoted reviews through the merge queue

Operate only on supplied reviews already at MERGE. Do not promote, advance,
override, resolve skipped conflicts, trigger releases, or change repository
protection as part of this invocation. Queue promotion and ruleset administration
are separate authorized operations.

No automatic drainer invokes this skill today. Use the installed erun commands and
their help for exact flags, and read the target repository's applicable AGENTS.md.

The landing sequence, end to end:

```text
erun exec gate-merge --target main --source <branch>... --under-lease <lease>
erun build --output json                       # plain build of the prepared merge commit
erun review record-build <id> --commit <merge-sha> --version <minted> [--failed --failure-detail ...]
erun exec push main                            # only if the build succeeded
erun exec report-commit-status ...             # only where a required check asks for it
erun review report-merged <id> --build-id <recorded-build-id> --remote-url <remote>
erun exec close-pr <branch> ...
```

There is no separate gate mode and no per-attempt run record. The merge commit's
build is an ordinary build: it runs every component's lint and tests, and a
component whose inputs are unchanged since its last successful build reuses that
result, which is a valid result rather than a skipped check.

## Preconditions and batch boundary

- Require erun, platform authentication, git access, and the build toolchain.
  Platform authentication is checked first and as a real check — see below —
  because this drive is built entirely out of platform calls. Report any other
  missing setup from the actual command's refusal; do not invent credentials.
- Resolve each review fresh: status, source, target, name, repository, and remote
  source SHA. Drop non-MERGE reviews without changing their state. Refuse mixed
  target branches, mixed repositories, and missing source refs: a queue belongs to
  one repository, and composing a review from another one runs `gate-merge` against
  a branch that need not exist in this checkout. `erun` derives the queue's
  repository from this checkout's origin; name it with `--repository` if this drive
  is not running in the repository the promotion targeted.
- The normal API allows one MERGE review per tenant/repository/target. Multi-source
  `gate-merge` support does **not** prove arbitrary multi-review acceptance.
  Do not promote extra reviews or fabricate successful per-review builds to
  bypass this boundary; API batch-verification design remains unresolved.
- Batch membership, landing order, selective retry/bisection, and failure ownership
  are caller policy. Git composition and known infrastructure classification are
  shared erun mechanisms, not shell implementations to duplicate.

### Stop before the claim if this machine has no platform access

Every rung of this drive needs the platform: rung 1 resolves each review's
status, source, target, and remote source SHA, rung 3 records the merge commit's
build, and rung 4 calls `erun review report-merged`. An agent environment
**cannot sign itself in** to an erun platform alias — `erun cloud init` succeeds
unattended, but `erun cloud login` needs a human at a browser for either of its
flows (Device Authorization Grant, Authorization Code + PKCE). An environment
`erun init` provisioned from an already-signed-in host carries one anyway and can
drive this itself; one that does not has no way to acquire it. So where there is
no alias the drive is a **credentialed-host operation**, not a pod operation, and
it must stop here rather than spending a run discovering that.

```sh
probe=0
erun review list --dry-run >/dev/null 2>&1 || probe=$?
if [ "${probe}" -eq 127 ]; then
  cat >&2 <<'EOF'
This machine cannot make erun platform calls, so this drive cannot start: no
usable erun platform cloud provider alias is configured.

Stop before taking the environment claim below. A claim taken here would
reserve this environment for a drive that can never record anything, and would
refuse the merge build that a credentialed host could actually run.

Do not try to acquire a platform alias here. `erun cloud init` succeeds
unattended, but `erun cloud login` does not — both of its flows need a human at
a browser, so no retry, timeout, or piped answer completes one. The only way
this environment gets one is `erun init` re-run from a host that is signed in,
which is not something this run can arrange.

The split: an environment builds (`erun build` needs no platform alias — with
none configured it skips reporting its outcome), and a credentialed host makes
every `erun review` call. Hand this drive to an orchestrator or operator
machine that has `erun cloud login` done and can reach this environment's
worktree, and let it claim the environment and run the drive.
EOF
  exit 127
fi
```

The probe resolves the alias without reaching the network (`--dry-run` stops
before any HTTP call), and 127 is erun's own documented exit code for "this
machine cannot resolve a usable platform alias" — see
`erun-docs/docs/cli/review.md` § Error behaviour. It is a check, not advice:
an agent cannot satisfy it by trying harder. Any other nonzero probe result is
deliberately left to the real call that reports it.

## 0. Claim the environment

Before any checkout or build dispatch, take an exclusive activity lease explicitly
scoped to `environment`, naming this drive and its owner. The default scope is
worktree and is insufficient for a heavy build.

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
  do not hand-roll its reset/merge loop or resolve a skipped branch here. A
  conflict confined to `atlas.sum` is not in `skipped` at all: the command
  regenerates it from the migrations already on disk and the source lands, so a
  batch member that landed despite a conflicting checksum needs no follow-up.
- A skip whose reason starts `regression-test policy:` is a defect fix whose
  reproduction declaration does not hold (root `AGENTS.md` § "A Defect Fix
  Names Its Reproduction"). Report the reason to the review's author, who
  amends the trailers and pushes; never land it any other way.
- An empty/all-skipped result is **not** a green build. Stop before building or
  pushing. Report real conflicts against the observed source commit; classify
  fetch/access/infrastructure failures separately instead of failing every review
  merely because stdout was empty.
- Preserve composition JSON, source SHAs, landing commits, order, and skip reasons
  as the attempt artifact. The merge commit is the composition's final `HEAD`;
  that is the commit rung 2 builds and rung 3 records.
- A conflict with previously squash-merged dependency content needs deliberate
  branch-history review by its owner; patch-equivalence guesses cannot reliably
  detect every multi-commit squash relationship.

**The binary running `erun exec gate-merge` here is the environment's installed
erun, not the checkout being landed.** It is a release, routinely one or two
behind, so a fix merged into erun's own landing path — the trailer carrier
included — is inert until this environment is upgraded, and the skew is
invisible in every other signal: the composition succeeds, the build passes, and
the old behaviour is simply still there. Read the version that actually ran
before concluding anything about such a fix, and name it in the report rather
than reporting a merged fix as failed:

```sh
erun version --no-registry   # the `erun <version>` line is the pod's binary
```

`erun-merge`'s build rung reads it too.

## 2. Build the merge commit

Renew the environment claim and run one plain `erun build --output json` of the
merge commit — never `--release`. The build publishes nothing; it mints the
version rung 3 records. Save its complete stdout/stderr and the composition under
the attempt's durable log/artifact location, and capture stderr separately:
a failed build may emit no JSON body, so take the failure detail from the tail of
its stderr.

Use a tracked job and explicit execution timeout when the build outlives one
command. Use bounded job awaits and read its own terminal state. Wrapper 124,
Make exit 2, or “still running” is a wait result, not a build verdict.
Keep the environment claim while recovering the actual result.

- A zero exit with a minted version is a successful build.
- A nonzero exit is a build failure. Whether it is a verdict about the change or
  an infrastructure non-verdict is decided in rung 3 by `erun review record-build`
  itself, through erun's shared classifier; do not match signatures here.
- A genuinely unknown result — a wait that never reached a terminal state after
  safe recovery checks are exhausted — is inconclusive. Record nothing and leave
  the review at MERGE.
- Preserve upstream status across pipelines and diagnostic tails so successful
  logging cannot hide failure. Do not retry a failed build blindly; a subsequent
  authorized attempt recomposes and builds again.

## 3. Record the build against the review

Record the build against the MERGE review at the merge commit it built, with the
version that build minted:

```sh
erun review record-build "${review_id}" --commit "${merge_sha}" --version "${version}" --output json
```

Keep the returned `buildId`: it is the only build rung 4 may name. For a failed
build, mint a same-form version with `erun build --dry-run --output json` (the
failed run printed none) and record it with `--failed --failure-detail` carrying
the build's own stderr tail. Never fabricate a commit or a version.

- A recorded failure moves the review from MERGE to FAILED. Stop: do not push.
- When the failure detail names a known infrastructure signature, `record-build`
  refuses to record it and the review stays at MERGE. That is a non-verdict, not a
  failure: report it with the matched signature and the remedy (re-run the build
  once the infrastructure recovers), and do not push.
- A successful record leaves the review at MERGE; only rung 4's verified report
  moves it on.
- For a legitimately supported batch, retain source-to-review/build/commit mapping
  and landing order; do not infer that the final-tip build automatically verifies
  every source alone.

If recording a successful build is refused for any other reason, preserve the
refusal and stop before pushing; the review stays at MERGE.

## 4. Push and report acceptance

- Verify the tree/HEAD still matches the built composition, then push the target
  once through `erun exec push`. Do not force, blindly retry non-fast-forward,
  or publish after a failed, refused, or inconclusive build.
- Where the repository requires the `erun/merge-gate` status check on its pull
  requests, report it with `erun exec report-commit-status` against the review's
  **source** tip (the pull request head), never the local squash commit.
- Only after the push lands, call `erun review report-merged` with the `buildId`
  rung 3 returned and the actual remote URL. Never pass the review's earlier
  `READY` build: it built the source tip, not the merge commit. Stop at the first
  refusal.
- The platform accepts `MERGED` only when that build is successful and belongs to
  the review, and its commit is reachable from the target's tip, descends from the
  last queue-merged target tip, and is not that tip itself. It does **not** require
  immediate-parent equality; release metadata commits may intervene. Respect a
  `MERGE_NOT_VERIFIED` refusal rather than inventing a success report.
- For each confirmed MERGED source, use `erun exec close-pr` with the built
  **source** SHA as `--gated-commit` and the actual **landing** SHA. A source with
  no open PR is a no-op; a moved PR head is a refusal, not permission to discard
  new work.
- Skip PR closure for any review whose acceptance did not succeed. Verify linked
  issue state after closure instead of assuming closing references fired.

Desktop Playwright is part of the build's checks, so no substitute desktop
attestation is needed. This does not prove native OS behavior outside that suite;
see `erun-ui/AGENTS.md`.

## 5. Release the claim and report

Release the same explicit environment scope and drive ID, including on failures.
Report review/source/target, landed/skipped state, the build's outcome, build IDs
and commits, push/acceptance results, PR closure, logs, and unresolved anomalies.
A review may remain MERGE after an infrastructure non-verdict or an inconclusive
wait: do not summarize every outcome as MERGED or FAILED.

Stop here. No release, next-queue promotion, or destructive worktree cleanup is
part of this skill's normal run.

## Resuming after partial failure

Read current reviews, remote refs, saved composition, and build records first.
Before a successful recorded build, a new authorized attempt can recompose and
build the remaining eligible sources; FAILED reviews need separate re-promotion.

After a successful recorded build or a partial push/acceptance, do not blindly
rerun the whole skill. Determine what actually landed and resume only missing
push/report/closure steps with the original verified IDs and order. Already
accepted reviews stay accepted. Do not record a second build of the same merge
commit or close a PR whose head moved.

## GitHub ruleset administration (separate authorization)

This is the operational home for the API guide's branch-protection policy, not
an extra rung this skill executes automatically.

- A raw merge-queue push needs a dedicated, narrowly scoped non-human GitHub actor.
  Required PR checks do not identify that push. Decide explicitly whether release
  publication shares the actor or uses its own; GitHub and platform credentials
  are separate trust paths.
- Use `erun exec plan-ruleset-bypass` to prepare a staged migration: first add the
  designated identity alongside existing access, prove its actual build/push path,
  then demote broad always/exempt bypasses to PR-only access. Do not delete all
  bypass actors, guess numeric role IDs, or create an exempt path invisible to
  the bypass ledger.
- Inspect plans before applying either stage. Read `current_user_can_bypass` with
  each actual credential after application; listing rules or a dry-run git push
  is not equivalent proof. Preserve existing actor entries not intentionally changed.
- Require a status check only after proving its producer reports the actual PR
  head SHA. A status on the prospective squash commit does not satisfy that check.
- Reconcile bypass rule-suite detail for the specific ruleset with
  `erun exec reconcile-bypass`, which accounts for a push by a successful recorded
  build of a commit it landed, or by a release tag. Expand push ranges and
  distinguish tagged release commits from queued merges. Flag an unexpected actor
  even when the pushed content was built. Registry release provenance is not a
  merge verdict.
- Account provisioning, secret distribution, ruleset writes, and broader-role
  demotion require their own approved scope; this guidance is not authorization.

## Release cadence handoff (separate authorization)

A merge can enqueue a release but no drainer claims/runs it automatically yet.
The proposed coalescing design lives under Release Queue in
`erun-backend/erun-backend-api/AGENTS.md`: newest queued commit, superseding older
rows after roughly 5–10 commits or 30–60 minutes, one running release per tenant,
with expiry/idempotency. These thresholds are provisional, not implemented timers.

When an authorized orchestration task includes releasing, use shared release
orchestration and verify publication before public refs. Bring the queue's
environment onto that published version in the same authorized workflow;
release itself does not deploy, and ordinary tenant upgrades remain discretionary.
Do not claim merely enqueueing a row delivered a release or upgrade.
