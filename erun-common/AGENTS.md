# AGENTS.md

Shared Go engineering guidance. Follow root `AGENTS.md`; CLI and MCP also read
this file for the conventions below.

## Module Role And Boundaries

- Keep this a standalone, transport-neutral library. Extract a stable shared
  responsibility, not a transport wrapper or a speculative abstraction.
- Reuse canonical contracts across callers, with neutral plan/params/result
  names. JSON tags are acceptable; Cobra and MCP SDK dependencies are not.
- Root "Command primitives vs orchestration" owns primitive policy. Shared
  resolution, execution, preview plans, and result assembly belong here;
  prompts, schemas, terminal formatting, and server wiring belong to transports.

## Preferred Direction

- Organize around cohesive responsibilities: contracts, planning, execution,
  discovery, formatting, and persistence. Mirror command names across transports
  when useful; do not use entrypoints or vague utility files as staging areas.
- Keep real composition boundaries thin. Separate read-model assembly from
  mutation, and contract types from long-running operations. Keep workflow state
  and its transitions together.
- Prefer pure functions, immutable plans, explicit runtime structs, and injected
  dependencies over globals. Mutable state belongs to one invocation.
- Local execution remains the default; remote/hosted transports are additive.
- Generate tenant runtime wrappers from shared templates. Keep them thin over the
  canonical runtime image; render stable identity explicitly and thread startup
  values through deployment plans rather than infer them from cwd or ambient state.
- Follow root "Refactoring Rules" for behavior preservation, ownership moves,
  visibility, and removal of obsolete wrappers.

### Platform identity: one per environment, and its form says which grant

- An environment carries **one** platform identity, delivered as the single
  `<tenant>-devops-platform-alias` Secret the runtime chart mounts. Which kind
  it is lives in the alias entry inside: a `refreshtokenref` is a signed-in
  person's delegated session, a `clientsecretref` is a machine identity erun
  provisioned for that environment, and `resolveERunAccessToken` picks the
  grant the reference implies. Never set both on one entry
  (`ERunProviderConfig.IsMachineIdentity`), and never read a credential down
  the other kind's path — a machine identity that acts as its operator, or a
  pod holding a person's session, is the failure this split exists to prevent.
- `platform_machine_identity.go` mints the machine form and
  `platform_alias_secret.go` the delegated one, over the same channel and at
  the same point in `erun init`; the machine form is asked for first and every
  way it can fail falls back to the delegated one, traced rather than silent.
  A credential is exchanged for a token **before** anything is written: a
  machine identity nothing accepts is worse than none, and provisioning is the
  only moment erun can tell the two apart.
- `EnvConfig.PlatformAliasSecretName` is a single scalar, so an environment is
  on one identity or the other and never both through a transition. Switching
  is a re-provision (re-run `erun init`), not a merge — roll-forward design is
  erun#2684's, and this shape is a constraint it has to work within.
- The platform machine identity and the GitHub queue identity are two trust
  domains. Name them coherently for attribution; never share the literal secret.

### Process, job, and deployment contracts

- Paths passed to other processes must not contain the desktop executable's name:
  process-name matching must not kill unrelated sessions. Keep
  `TestSessionSocketPathCannotCollideWithTheDesktopBinary`.
- Wrap agent streaming modes and normalize vendor events into `AgentJobProgress`
  (`job_agent.go`). Callers must not scrape vendor transcripts or depend on vendor
  event names. Persist useful failure reasons, not only exit codes; retain the
  underlying error for exit-code matching (`build_failure_reason.go`).
- `job_exclusive.go` protects gates with an environment-wide claim, refusing
  ordinary jobs as well as exclusive ones. Worktree scope alone cannot isolate
  CPU/memory. Preserve self/descendant exemptions through `StartedByJobID` and
  explicit `underLeaseID` for a multi-process owner; neither may bypass another
  owner's claim. A presence lease is not an exclusive claim.
- A supervisor has one mutex-guarded record writer (`jobRecorder`), including
  progress ticks and terminal outcomes. Never let a late progress write replace
  the final result. Lease renewal, expiry, supervisor reconciliation, and the
  maximum lifetime must remain bounded.
- A supervisor that ends before it registers leaves no record, so the start
  call is the only surface that can say why — and the only process that knows
  is the supervisor itself. Its stdout and stderr are therefore captured to the
  job's log rather than discarded to the null device
  (`spawnEnvironmentJobSupervisor`), and the start failure quotes that capture
  back (`jobSupervisorStartupFailureCause`). An empty capture is a real answer:
  a supervisor killed outright never got to say anything, and the bare
  "exited without registering" is then the whole truth — never invent a cause
  for it. The post-registration flavour is the other half of this and is
  `recordEnvironmentJobSupervisorFailure`'s, not this path's.
- Agent reinvocation is bounded recovery, not a general retry loop:
  `decideEnvironmentJobReinvocation` accepts only an agent with a captured session
  ID whose started work was incomplete or failed. Reuse the same job, supervisor,
  lease, counter, and deadline; never reset bounds when the resumed turn starts
  more work. Defaults are two resumptions and 30 minutes
  (`EnvironmentJobMaxReinvocations` / `EnvironmentJobReinvocationBudget`).
  Surface the count in status. A plain nonzero exit without started work is not
  eligible. Claude continuation was verified live; Codex context survival remains
  a disclosed live-verification gap, not a reason to remove its resume support.
- Keep dtach for interactive sessions. The evaluated native background lifecycle
  lacked reliable passive logs, non-interactive input, and cross-tool takeover
  parity; revisit only when those guarantees are verified. This decision does not
  prohibit non-interactive job resume. Build launch commands in
  `AISessionLaunchCommand`; do not add `--fork-session` on reattach.
- Host bootstrap may seed missing pod config, never overwrite existing values.
  Environment-owned reconciliation belongs to `doctor --sync-config`.
- An in-cluster cloud context names the cluster the current process itself runs
  in, so it is already running: `CloudContextPreflight` must not refresh or
  start it, and no working-hours gate applies (`isInClusterCloudContext` in
  `cloud_context.go`, `cloud_context_in_cluster_test.go`). The default a pod's
  injected env produces is that same sentinel. A power-managed context with no
  instance ID is still a genuine `has no instance ID` error — the distinction
  is in-cluster versus power-managed, never present versus missing.
- Route an off-environment operation to where its state lives without requiring
  an interactive shell. Remote dispatch is routing, not convenience orchestration.
  Confirm mutations before dispatch, then pass the resolved confirmation to the
  non-interactive child (`terraform.go`).
- Chunk downloads across exec streams, accounting for base64 expansion in the
  payload bound; carry the first range with metadata and verify the reassembled
  digest against the pre-transfer digest (`outputs_download.go`). Do not remove
  login-shell initialization globally to optimize one transfer path.
- Network diagnosis needs both a matching transport-stall signature and a measured
  mismatch in the same namespace (`build_network_mtu.go`). A server response such
  as 404 is not network evidence. Warn on risk; refuse only on established failure.

### Execution-mode exceptions

Library replacement must preserve the real tool's semantics and audit trace,
not merely a similar final state. Keep these subprocesses until equivalence is
demonstrated:

- `git merge --squash`: strategy, rename, and conflict-marker semantics.
- `dtach`: detachable PTY and owner/takeover outcomes.
- `aws sso login` / `aws configure set`: browser flow and shared config writes,
  not ordinary SDK requests.
- `helm upgrade`: watcher-driven interruption and kill on early container failure.
- `docker build`: fingerprint/cache semantics and BuildKit failure output.

### Build fingerprints

- The fingerprint's input set must match the real Docker context. Changes to
  ignore handling and Dockerfile parsing require regression coverage for both.
- Local `ADD`, globbed `COPY` sources, and external-image `COPY --from` are
  unsupported fingerprint inputs. Keep the prohibitions in
  `dockerfile_copy_contract_test.go` until support is implemented, not merely
  until a new Dockerfile needs them.
- Explicit pinned base tags are required. They mitigate, but do not eliminate,
  mutable-tag drift: the fingerprint hashes the tag, not its live registry digest.
  Do not claim the static tests prove digest stability.

## Gate execution and verdicts

- Long routine validation uses `scripts/agent-gate.sh`: direct execution outside
  agent pods, tracked detachment plus bounded await inside them. Keep inner
  check-gate prerequisites on the underlying targets so they do not detach twice.
  Extend by runtime needs, not target name. Release callers supervise their own
  jobs; release is not automatically wrapped. Run `scripts/agent-gate_test.sh`
  when changing the wrapper.
- `scripts/parallel-gate.sh` owns quota/memory-aware fan-out sizing. Independent
  fan-outs must budget aggregate memory, including race-enabled test overhead,
  not each reserve the whole ceiling. Run its script tests when changing it.
- Batch gate-merge takes ordered sources, fetches once, and composes them on one
  prospective tree. On a squash conflict, restore the batch's last committed
  state, record skipped files/reason, and continue. Refuse an all-empty batch.
  Do not loop single-source invocations that reset away earlier work.
- Keep one gate_run per batch and preserve ordered landed/skipped composition
  in its log artifact. Mapping one batch build to multiple review acceptances
  remains an API design question, not an execution shortcut.
- `gate_run_failure_classifier.go` owns infrastructure signatures. Start/report
  inspect failure text and bounded local log content, visibly reclassifying known
  infrastructure failures as INCONCLUSIVE. A failed review GATE build with the
  same signature must be refused instead of changing the review to FAILED;
  its boolean success field cannot represent a non-verdict. Test both records.
- Release cadence/coalescing is an unwired design in
  `erun-backend/erun-backend-api/AGENTS.md` § "Release cadence policy".
  Do not treat drift reporting as an automated release drainer.
- A build's own trace states what the builder did with its Dockerfile's `test`
  stage, not what the plan intended. `dockerfileHasGateTestStage` keeps a gate
  image out of the *fingerprint* promotion path, but BuildKit's layer cache sits
  underneath that guard and can serve the whole test stage on its own: the build
  finishes in seconds at zero CPU, `make check` never executes, and the exit code
  is 0 — the same green as a real gate run. Two mechanisms close it, and neither
  is optional. A run that declares itself the merge queue's gate
  (`ForceGateTestStage`, set from `--gate`) makes its own `docker build`
  invalidate just that stage (`--no-cache-filter`, `dockerBuildGateStageArgs`), so
  declaring a gate costs the gate and not a cold rebuild of the shared cache. A
  wholly replayed stage is otherwise **refused by every build, not only by
  `--gate`** (`ensureGateTestStageExecuted`): both documented gate flows — the
  merge queue's `gate-merge` → build → `record-build --gate`, and the erun-merge
  skill's READY rung — run plain builds, so a guard the flag arms never covered
  the run it was written for. Do not re-key that refusal on a flag.
  `gateTestStagePlanLines` announces the
  plan and must not use the outcome vocabulary; `gateTestStageProvenanceLines`
  reports the outcome — LIVE, CACHED or REPLAYED — read from the builder's own
  captured `--progress=plain` stream, which is the one report a warm cache cannot
  fake (an in-image marker is replayed along with the layer holding it). Keep the
  stage's `FROM` step out of that evidence: BuildKit reports it DONE even when
  every instruction below it was CACHED, which would hide the replay
  (`build_gate_test_stage_evidence.go`).
- Preserve the gate wrapper's distinction between a clean pass, an exit-zero
  process that left unsupervised work (reported with an explicit warning and job
  ID), and a genuine failure. The orphan warning is not proof of completed work;
  callers must inspect the job's own record. A wrapper's bounded-wait timeout is
  also not the underlying gate verdict (`scripts/agent-gate.sh`).
- A recorded pass is replayed only for the run that produced it: its tree, its
  command, and the environment it executed under. The environment travels with
  the record instead of in the job id -- the id is also what a re-invocation
  after an expired bounded wait attaches to, so moving it with the environment
  would find no job there and start a second gate beside the first. A recorded
  pass from a differing environment is named loudly and run fresh, never
  replayed: keyed on the whole environment minus the wrapper's own control
  variables, so a knob nobody thought to enumerate still separates two runs --
  and so does the pod, whose name the container puts in HOSTNAME, so a recorded
  pass does not survive the pod that produced it being replaced. A run still
  *live* under a differing environment is the one case the wrapper cannot run
  fresh, since the exclusive claim refuses a second gate beside it: it names
  the mismatch loudly and attaches, reporting that run's verdict as that
  environment's and not the caller's.
- A wait expiring is the wrapper's own deadline, never the gated job's outcome.
  On expiry the wrapper reads the job's own record, so a job that finished is
  reported by its actual result and 124 is reserved for one still genuinely
  running; an unreadable record is that same non-verdict, never a failure. That
  non-verdict path must say so plainly and name the job, since the exit status
  alone is not readable as "not a failure" once `make` is in between: GNU Make
  collapses any nonzero recipe exit to its generic exit 2, so a caller reading
  only `make check`'s exit status cannot tell a bounded-wait timeout from a real
  failure. The `check` target therefore prints INCONCLUSIVE on 124, and the
  wrapper's own 124 survives only for a direct caller.
- Keep the default foreground-safe (bail at the first timeout) and let a caller
  that is not foreground-constrained opt in with `AGENT_GATE_AWAIT_VERDICT=1`,
  which re-awaits the same job across bounded `job await` calls until it reaches
  a real verdict. `ERUN_JOB_ID` being set does not distinguish the two callers.

## Release recovery

- Preserve disk-headroom preflight before expensive work: reclaimable build-cache
  pruning and refusal on a measured shortage, but no invented refusal when the
  daemon's filesystem cannot be observed. The default/tuning live in
  `release_disk_headroom.go`.
- Report already-published target artifacts before rebuilding with a single probe;
  reporting must not replace fingerprint-based promotion or imply a new resume engine.
- A push the registry rejects for a blob it does not hold is the concurrent-publisher
  shape, not a local defect: two releases sharing layers can have the loser's manifest
  rejected while the peer's upload is still committing. `DockerImagePusher` re-pushes
  it, bounded, gated on `IsDockerUnknownBlobError` alone. Do not add a second retry
  for it at a higher layer and do not widen the predicate — an auth, policy, or network
  failure must still surface on its first occurrence. The promote path's
  rebuild-from-source fallback remains the deeper recovery for the one blob rejection
  a re-push cannot clear: a stale local "already pushed" record that skips the upload
  again.
- Refuse an existing release tag at a different HEAD. If it is an unpushed,
  unincorporated interrupted-run tag, name that diagnosis and the explicit remedy;
  never automatically delete it. Preserve retryable version state.
- Recheck the remote branch before building and reconcile a later move through
  bounded final-push recovery. Human scheduling cannot replace those checks.
- Scope final-push recovery to the ref git actually rejected. One push carries
  the base branch, develop and the tag, and only the base branch's own rejected
  ref is repaired by rebasing it. A rejection on any other ref is reported as
  what it is — the base branch did not move, so rebasing it is a no-op that
  spends every attempt without ever fetching the rejected ref
  (`release_remote.go`, `release_remote_push_rejection_test.go`). Keep the
  integration golden that absorbs a base branch that genuinely moved.
- Treat a final-push failure as post-publication, not as an interrupted release:
  the images, charts and tag are already public, and the GitHub Release object is
  created after the push, so it is absent. The failure must name the ref that did
  not land, git's own reason, and the missing Release object as the gap; it must
  never read as the pre-publication shape whose recovery deletes the tag and
  resets the branch (`real_run_names_the_rejected_develop_and_does_not_rebase_main`).
- Distinguish pod replacement from a missing supervisor in the same pod using
  the recorded pod identity (`EnvironmentJob.UnknownReasonKind`), not exit-code
  guesses. Preserve that cause through status and recovery reporting.

## Dependency Wiring

- Wire concrete defaults at the real composition boundary. Pass only values a
  function uses, not a dependency bundle it merely forwards.
- Pass already-built handlers/services/subcommands directly. Do not add a local
  alias for one concrete call; a local binding is useful only when reused.
- Keep test-only wiring in clearly named `_test.go` helpers.

## Visibility

- Default to package-private; export only for a current external caller, not
  same-package tests. Lower a symbol when its last external use disappears.

## Naming

- Prefer direct nouns over a default `Service` suffix; reserve that type suffix
  for a real domain service.
- Use `Params` for local input structs with fewer than five top-level fields.
  Reserve `Request`/`Response` for transport or genuine request/response contracts.

## Go Safety Notes

- Value copies of slices, maps, pointers, channels, or containing structs still
  share state. Clone when callers must not mutate the owner's data.
- Keep resource lifetimes and concurrency ownership explicit. Root boundary-data
  and applied-state rules also apply to shared plans and persisted results.

## Diagnostic Decision Record

The surfaces that report state to an operator -- CLI output, log lines, error
messages, and the panels that render them -- follow these decisions. Every one is
already honoured somewhere in this codebase; the value of writing them down is the
one the UI record in `erun-kit/AGENTS.md` already demonstrates. A reviewer cites a
decision instead of re-deriving it, and a departure becomes a note rather than a
drift. `erun-cli/AGENTS.md` and `erun-ui/AGENTS.md` apply this record to their own
surfaces.

- **A failed read states what could not be read, in the operator's terms, before
  the underlying error.** `erun-ui/runtime_usage.go` and
  `erun-ui/environment_usage.go` both lead with `Cannot read this environment's
  resource usage: ` and let the cause follow; `erun-ui/runtime_sizing.go` does the
  same for a sizing recommendation. A bare `exit status 1: kubectl ...` with no
  headline is the departure this names.
- **A message names the command actually invoked, or names none.**
  `erun-common/open.go`'s shared tenant resolver hardcodes its own caller --
  `open could not infer one` -- so `erun usage` and `erun observe` both report that
  `open` failed (#2363). A command that names itself must be the one the operator
  ran; where a shared helper genuinely cannot know its caller, it names no command.
  `erun-cli/cmd/pin.go`'s `` see `erun pin --list` `` is the honoured form.
- **A failure that occurred once is reported once, at the level it occurred.**
  `erun-common/cloud_context.go`'s `setCloudContextStatusFailure` builds one
  `status refresh failed: ...` string outside the loop and stamps it on every
  index, so a single batched `describe-instances` failure is repeated verbatim per
  context (#2365). The error belongs once, on the call that failed.
- **A no-op does not spend a line from a bounded budget.**
  `erun-common/workspace_sync.go` already suppresses both halves of this: a missing
  or empty outputs dir is a no-op, and a pass whose content is unchanged transfers
  nothing. A periodic pass that emits a fixed line per iteration whether or not
  anything changed spends a bounded retention window on no-op content (#2346).
- **A state that can be entered can be exited, and both transitions are recorded.**
  An outage written on entry and never on exit leaves a resolved outage
  indistinguishable from an ongoing one after the fact (#2348).
- **An error state offers the recovery action where the app owns it.** The UI
  record already requires this; the rule is not confined to the UI. The honoured
  form names the next command (`pin.go`), the flag that disambiguates, or the setup
  step (`open.go`'s `` run `erun init --tenant <name> --set-default-tenant` ``).
  Offering no recovery is correct only where the operator has none.

### Recorded inconsistencies

These are live disagreements between surfaces, not decisions. They are recorded
rather than resolved so that the next change picks a direction deliberately
instead of adding a fourth treatment.

- **Three read-failure treatments in one tab.** The Runtime tab's three panels
  render amber-plus-icon, plain grey, and amber-without-icon, with a `role=alert` /
  `role=status` split (#2310). The alert/status split is a real distinction --
  attempted failure versus expected block -- and the icon and tone do not follow
  it.
- **One panel, two states, opposite care.** The API log's empty state is written
  for an operator; its error state is a bare tool error with no headline (#2355).
  The first decision above already covers the error state; the panel has not been
  brought across.
- **Four read-failure surfaces, four treatments** -- headline+retry,
  headline+refresh, headline only, neither (#2355). Retry versus refresh is a
  property of the read, not of the panel, and is not decided anywhere today.

### What this record still owes

The record has no citation habit yet: no decision here is named in a code comment,
because none existed until now. Whether a diagnostic departure is caught in review
is therefore untested. The UI record earned its enforcement from three issues filed
against violations of it (#1418, #1419, #1420); this one has not been through that
yet, and should not be assumed to hold until it has.

## Validation

- After Go changes run `go test -count=1 -race ./...`. The root
  `test-erun-common` target runs it in `check-gate`; race coverage is essential
  for leases, job supervisors, and workspace sync.
- Exercise binary-reachable behavior through integration scenarios; remove unit
  tests duplicating those scenarios. Keep focused tests for otherwise unreachable
  behavior and concurrency. See `erun-integration/AGENTS.md` § "Known integration
  coverage gaps" for accepted limitations.
