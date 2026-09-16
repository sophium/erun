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
- Preserve the gate wrapper's distinction between a clean pass, an exit-zero
  process that left unsupervised work (reported with an explicit warning and job
  ID), and a genuine failure. The orphan warning is not proof of completed work;
  callers must inspect the job's own record. A wrapper's bounded-wait timeout is
  also not the underlying gate verdict (`scripts/agent-gate.sh`).

## Release recovery

- Preserve disk-headroom preflight before expensive work: reclaimable build-cache
  pruning and refusal on a measured shortage, but no invented refusal when the
  daemon's filesystem cannot be observed. The default/tuning live in
  `release_disk_headroom.go`.
- Report already-published target artifacts before rebuilding with a single probe;
  reporting must not replace fingerprint-based promotion or imply a new resume engine.
- Refuse an existing release tag at a different HEAD. If it is an unpushed,
  unincorporated interrupted-run tag, name that diagnosis and the explicit remedy;
  never automatically delete it. Preserve retryable version state.
- Recheck the remote branch before building and reconcile a later move through
  bounded final-push recovery. Human scheduling cannot replace those checks.
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

## Validation

- After Go changes run `go test -count=1 -race ./...`. The root
  `test-erun-common` target runs it in `check-gate`; race coverage is essential
  for leases, job supervisors, and workspace sync.
- Exercise binary-reachable behavior through integration scenarios; remove unit
  tests duplicating those scenarios. Keep focused tests for otherwise unreachable
  behavior and concurrency. See `erun-integration/AGENTS.md` § "Known integration
  coverage gaps" for accepted limitations.
