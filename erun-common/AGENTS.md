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

### Package layers

The module is split into layers so an edit recompiles only its dependents and
the compiler enforces primitive independence. The module root holds no package;
new code goes into the layer it belongs to:

```
erun-common/
  base/          model/ core/ config/ seam/
  infra/         kube/ helm/ gitx/ registry/ platformclient/ cloud/ activity/ mcp/ mcpauth/ mcptools/ remoteinit/ codesign/
  primitive/     shared/<concern>/ build/<concern>/ deploy/<concern>/ open/<concern>/ push/ release/
  orchestration/ build/ deploy/ release/ execute/ clusterread/ diagnosis/
  command/       ai/ doctor/ e2e/ expose/ inputs/ jobs/ lifecycle/<concern>/ mergequeue/ observe/ orchestrator/ platform/ podruntime/ review/ setup/ terraform/ workspace/
```

Every package may import the Go standard library. Beyond that, a package may
import only the erun packages its row names:

| Package | May import |
|---|---|
| `base/model` | no other erun package |
| `base/core` | `base/model` |
| `base/config` | `base/model`, `base/core` |
| `base/seam` | `base/model`, `base/core`, `base/config` |
| `infra/*` | `base/*`; another `infra/*` only where its depguard rule names it |
| `primitive/shared/*` | `base/*`, `infra/*`, other `primitive/shared/*` |
| `primitive/*` | `base/*`, `infra/*`, `primitive/shared/*`, and for build, deploy and open their own `<concern>` siblings; never another primitive |
| `orchestration/build`, `/deploy`, `/clusterread`, `/diagnosis` | `base/*`, `infra/*`, `primitive/*` |
| `orchestration/release` | `base/*`, `infra/*`, `primitive/*`, `orchestration/build` |
| `orchestration/execute` | `base/*`, `infra/*`, `primitive/*`, `orchestration/build`, `/deploy`, `/release` |
| `command/*` | `base/*`, `infra/*`, `primitive/*`, `orchestration/*`, and for lifecycle its own `<concern>` siblings; never another `command/*` |
| erun-backend-api | `base/*`, `infra/cloud`, `/mcpauth`, `/mcptools`, `/platformclient` |

- `base/model` holds the data types modules exchange: every type another
  module uses, with the data types those reference. It declares types, their
  constants and methods only; behavior lives in the package that uses it.
  `erun-integration`'s `TestModelHoldsOnlyData` fails on a free function or
  package variable there.
- `base/seam` holds the func and interface types one package injects into
  another (stores, runners, resolvers), so a package that takes a seam depends
  on `base/*`, not on the package that implements it.
- depguard in `.golangci.yml` enforces the table: each package denies the
  module and allows only what lies beneath it. An infra package lists the exact
  infra packages it imports, so a new edge between external systems is a
  reviewed config change. erun-backend-api's own `.golangci.yml` holds its row.
- `.go-arch-lint.yml` in every Go module lists, per package, the packages and
  third-party modules it imports. A new import is declared there in the same
  change, so a package's fan-out only grows as a reviewed decision. The build
  runs `go-arch-lint check`, `go mod tidy -diff` and golangci-lint (with
  revive's 500-line `file-length-limit`) in each module's test stage.
- The command rule holds production files only. A test may import a sibling
  command where it pins a contract between the two, and only there:
  `expose` → `observe` (`TestSharedReadsNameTheReadAndTheOperationNamesItself`),
  `orchestrator` → `ai` (the launch effort levels), `platform` → `setup`
  (`TestErunRegistry*`), `podruntime` → `doctor`
  (`TestRuntimeBootConfigCloudContextDefaults`).
- erun-cli's commands follow the same rule one level up: `erun-cli/cmd/<command>`
  per command, `erun-cli/cmd/shared/<concern>` for what two or more of them use,
  and the root `cmd` package registering them. erun-cli's `.golangci.yml` holds a
  command to `cmd/shared/*` and `cmd/shared/*` to no command. Split with the same
  tool, run from `erun-cli/cmd` (`maps/06-cli.map` names the command).
- A test whose helper is this test binary re-executed registers that helper
  from an `init` in its own file (`reentryHelpers`, in each package's suite
  `TestMain`), so it keeps working from whichever package its file is in.
  Tests that read a sibling module find the repository with `erunRepoRoot`,
  never by a fixed `../` depth.
- Move declarations between packages with `go run ./cmd/pkgmove`, never by
  hand. `plan -map` simulates a map (the import graph it makes, every cycle
  with the references that hold it, the fields it would export); `apply`
  rewrites this module and its importers. Each step's map is committed under
  `cmd/pkgmove/maps/`; a branch that predates a step re-runs that step's map
  after rebasing rather than resolving the move by hand.
- Test helpers are not shared across packages. A `_test.go` declaration
  cannot be exported, so a helper a moved test needs is copied into that
  package's tests, and moved instead when its own package no longer uses it.
- A primitive never calls another primitive: what two primitives share goes to
  the `primitive/shared/<concern>` package for that concern, and a composition
  of primitives goes in the
  `orchestration/*` package for that composition.
  Types and config code never call build, deploy or release.
- `cmd/buildcost` measures what one edit costs every module's build, vet and
  test compile; compare a layer's edit before and after its step.

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
- Keep dtach for interactive **pod** sessions. The evaluated native background
  lifecycle lacked reliable passive logs, non-interactive input, and cross-tool
  takeover parity inside a runtime pod; revisit only when those guarantees are
  verified there. This decision does not prohibit non-interactive job resume, and
  it does not cover host orchestrators, which run as Claude background agents
  attached from a PTY the desktop owns (`erun-ui/AGENTS.md`). Build launch
  commands in `AISessionLaunchCommand`; do not add `--fork-session` on reattach.
- Host bootstrap may seed missing pod config, never overwrite existing values.
  Environment-owned reconciliation belongs to `doctor --sync-config`.
- An in-cluster cloud context names the cluster the current process itself runs
  in, so it is already running: `CloudContextPreflight` must not refresh or
  start it, and no working-hours gate applies (`isInClusterCloudContext` in
  `cloud_context_power.go`, `cloud_context_in_cluster_test.go`). The default a pod's
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

- Batch gate-merge takes ordered sources, fetches once, and composes them on one
  prospective tree. On a squash conflict, restore the batch's last committed
  state, record skipped files/reason, and continue. A conflict confined to
  atlas.sum is the exception: it is regenerated from the migration files on disk
  and the source lands rather than being skipped. Refuse an all-empty batch.
  Do not loop single-source invocations that reset away earlier work.
- Preserve a batch's ordered landed/skipped composition in its gate-merge
  output. A batch lands as one merge commit build; mapping that build to
  several review acceptances remains an API design question, not an execution
  shortcut.
- `build_failure_classifier.go` owns erun's known infrastructure failure
  signatures (`BuildInconclusiveSignatures`: registry/network blips and a loaded
  Docker daemon failing Testcontainers startup). `review record-build --failed`
  refuses a failure detail matching one instead of moving the review to
  FAILED: `builds.successful` cannot represent a non-verdict, so the remedy is
  to re-run the build and record that result.
- Release cadence/coalescing is an unwired design in
  `erun-backend/erun-backend-api/AGENTS.md` § "Release cadence policy".
  Do not treat drift reporting as an automated release drainer.
- Testing is part of building. A component's Dockerfile runs its lint and
  tests in a `test` stage that a later stage consumes, so no image exists
  unless they passed. An unchanged component reuses its last
  successful result: a fingerprint tag is written only after a successful
  `docker build`, so promoting from it (`applyIncrementalPromotion`) is reuse
  of a tested result, and a BuildKit layer-cache hit on byte-identical inputs
  is equally valid. Every image, tested or not, takes the same promotion path
  (`build_test_stage_test.go`); an all-promoted plan is a successful build.
  There is no `--gate` build and no GATE build kind for new builds: the merge
  queue builds the composed merge commit with a plain `erun build` and records
  it RECORDED with its version.
- `dockerfileDeclaresTestStage` scopes the host-network entitlement to
  Dockerfiles with a `test` stage. Keep the stage name exact (`AS test`, not a
  `test-` prefix). A declared `test` stage must be consumed by a later stage
  (`FROM test`, `COPY`/`ADD --from=test`, or `RUN --mount=...from=test`):
  `checkDockerfileTestStageConsumed` (`build_order.go`) refuses one that is not
  when the build spec resolves (`newDockerBuildSpec`), so `erun build`, `erun
  push` and a component deploy's build all refuse it before anything builds.
  erun does not trace which stages the final image reaches; the repository's
  own stricter shape (runtime copies only from `test` or stages `FROM test`) is
  pinned by `dockerfile_copy_contract_test.go`, not imposed on tenants.
  `build_component_boundary_test.go` locks the consumer forms and the refusal.
- After each successful real build, every component reports one outcome line,
  `<component>: test: passed`, `test: reused (fingerprint fp-<hash>)` or
  `test: none declared` (`dockerBuildTestOutcome`), keeping a fresh run
  distinct from a promotion. A promote that falls back to a rebuild reports
  `passed`; failed builds and dry runs report nothing. Never collapse those
  outcomes into one.
- A Linux package component keeps no image to promote, so a plain build
  records its pass in `~/.erun/package-tests/<name>.json`, keyed by a
  fingerprint of the stages that ran (same hashing as images) and the platform
  the docker daemon builds for (`docker version`, asked once per build; never
  the host's CPU, which a remote `DOCKER_HOST` need not share), and the next
  plain build with that fingerprint reports `test: reused`
  (`linux_package_reuse.go`). A package with only a `build` stage reports
  `test: none declared` on every build, reused or not. An unreadable daemon
  platform reuses and records nothing. Only a successful real build records;
  `--release` never reuses and `--no-incremental` neither reuses nor records.
- `checkNotE2ESuiteDockerfile` refuses a Dockerfile under `*-devops/e2e/` for
  build and push, pointing at `erun e2e --component <suite>`; project-wide
  discovery walks only `docker/` and never reaches it. An e2e suite must never
  be fingerprinted, promoted or pushed.
- `RunE2E` (`e2e.go`, discovery in `e2e_discovery.go`) runs each
  `<tenant>-devops/e2e/<suite>/Dockerfile` as `docker build --target e2e
  --no-cache-filter e2e --output type=cacheonly` from the project root, with
  `ERUN_E2E_VERSION`, `ERUN_E2E_BASE_URL` and `ERUN_E2E_URL_<SERVICE>` build
  args and the `e2e.secrets` list. Keep those secrets separate from
  `docker.secrets`, which reach every component build. Build, deploy, release
  and the merge gate must never call it; it reports a failure bug-ready and
  files nothing. The contract is `erun-docs/docs/agent-reference/dockerfile-stages.md`
  § "The `e2e` stage"; the CLI goldens under `erun-integration/testdata/e2e/`
  lock it.
- A component's test stage only calls the module's own commands (`go tool
  golangci-lint run`, `go test`); it holds no test logic of its own.
- Long validation inside an agent pod runs as a detached `erun exec job start`
  awaited with bounded `job await` calls (root § "One Agent Job Is One Run").
  A wait expiring is the caller's own deadline, never the job's outcome: read
  the job's own record before reporting a verdict, and distinguish a clean
  pass, an exit-zero run that left unsupervised work, and a genuine failure.

## Release recovery

- Preserve disk-headroom preflight before expensive work: reclaimable build-cache
  pruning and refusal on a measured shortage, but no invented refusal when the
  daemon's filesystem cannot be observed. The default/tuning live in
  `release_disk_headroom.go`.
- A build cache is bounded per environment by one shape: this environment's share
  of its own docker volume, tightened by its share of the node and never replaced
  by it (`resolveBuildCacheBound`). A node share that resolves to a ceiling too
  small to hold a working set is not a small bound — the ceiling is what
  `docker buildx prune --max-used-space` reclaims the cache down to on every
  build, so a cache held under what a build is served from drops the layers the
  next build would have used every time it fires, exactly as it does at a ceiling
  of no bytes — so an unusable node budget leaves the volume share in force and
  is traced rather than acted on. What is unusable is read off the division the
  allowance itself came from rather than asserted as a size: the node's room above
  its reserve is divided among the caches that share it, and that reserve is
  divided the same way, so a ceiling under this environment's share of the reserve
  is a cache bounded smaller than the non-cache state beside it on the same disk
  (`resolveBuildCacheNodeAllowance`). The threshold is a refusal, not a floor to
  clamp up to: a CPU limit is shared fairly by the kernel, so
  `RuntimeDindCPULimit` can floor one, while a disk ceiling is space the
  environment then holds against every other tenant of that node, so inventing
  room a node has said it does not have re-creates the aggregate defect the node
  bound exists to remove. Refusing invents nothing; it leaves in force the bound
  that was already there.
- The node's co-tenant count is a declared chart value nothing cross-checks: the
  pod's RBAC is namespace-scoped, so it cannot count its own co-tenants. The trace
  reporting a node-derived ceiling names the count it divided by, which is the only
  surface where a declaration that has drifted from the node is visible
  (`build_cache_retention.go`, `build_cache_node_bound_test.go`).
- That node bound has two inputs, both opt-in, and both environment variables
  because neither is readable from inside the pod: `ERUN_BUILD_CACHE_NODE_BYTES`
  is the node's disk in bytes, `ERUN_BUILD_CACHE_CO_TENANTS` is how many build
  caches share it, and the chart renders the pair from its `buildCacheNodeGi` and
  `buildCacheCoTenants`, both defaulting to zero so neither variable is emitted.
  Unset is not a node budget of zero bytes: it is no node budget at all, and the
  environment keeps byte-for-byte the docker-volume ceiling it had, untraced,
  because that is the state every environment was in before this bound existed
  (`errNoBuildCacheNodeBudget`). A node size without a count is an incomplete
  declaration rather than a count of one — the chart emits the pair together, and
  `declaredBuildCacheNodeAllowance` refuses the count-less form instead of handing
  one environment the node.
- The node's share is `(node − floor) / coTenants`, where the floor is
  `resolveMinDiskHeadroomBytes` of the node itself — 20 GiB or 10% of it, whichever
  is larger, overridable by `ERUN_RELEASE_MIN_DISK_HEADROOM_BYTES` — and its
  ceiling is 80% of that (`resolveBuildCacheBounds`). It replaces the volume share
  only where that ceiling is the lower of the two, so declaring both inputs at a
  co-tenant count whose share still exceeds the environment's declared docker
  volume installs a declaration that changes no ceiling and is never named as one:
  the trace, and the co-tenant count in it, appear only once the node's share is
  what is applied. That count is `(node − floor) / volume`, and it is a function of
  the node, the floor and the volume in hand rather than a fixed number — work out
  which side of it a node falls on before setting the pair, rather than reading the
  declaration itself as a bound in force.
- A share is also refused where no co-tenant count can make it usable. The floor is
  divided by the same count the node's room is, so below `2.25 × floor` no count makes
  the share usable: while the floor is the flat 20 GiB — every node under 200 GiB —
  that is a node under 45 GiB, and a node at 21 GiB refuses the share at a count of 1
  as surely as at a million. The refusal is the disposal argument above and not the
  undeclared case: the volume share holds, and the run says the sum across the node
  went unbounded by it rather than passing over the declaration in silence
  (`errBuildCacheNodeBudgetUnusable`).
- That gate bounds the node and not the ceiling, so it is a floor on the node rather
  than on the bound — but the floor is not a clean node-only threshold. At exactly
  `2.25 × floor` the two compared quantities are equal and the division on both sides
  is integer division, so at the line the co-tenant count decides and not the node: a
  45 GiB node installs the share at counts of 1, 2, 4, 8, 16, 32 and 64 and refuses it
  at 3, 5, 6, 7 and 1000, each refusal 16–53 bytes short of the tie. The same rounding
  refuses shares above the line where the sides are close, so a 46 GiB node with 5e7
  co-tenants refuses a 400-byte ceiling against a 429-byte reserve share. Do not
  restate the guard as `node < 2.25 × floor`. Above that noisy edge the guard does
  stop bounding the ceiling: a 100 GiB node declaring 1000 co-tenants installs a
  68,719,440-byte ceiling, larger than the 21,474,836 bytes it is weighed against.
  Closing that would need a measured working-set floor, which the design deliberately
  does not assert — so the guard's refusals are not a size band and none of the byte
  counts in them is a floor; the sizes refused (up to 7,158,278,800 bytes on a 45 GiB
  node at three co-tenants) and the size installed are not ordered by magnitude.
  Refusing the share instead leaves every declared co-tenant unbounded by the node —
  the aggregate defect this bound exists to remove. The operator's remedy is the
  declaration: a count that shrinks the share that far is a count the node cannot be
  holding, and nothing in the pod can check it
  (`build_cache_node_bound_test.go`, whose refusals are exercised on 21 GiB and 32 GiB
  nodes only — the 45 GiB and 46 GiB edges above are measured, not pinned).
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
  same-package tests. Another erun-common package is an external caller: a
  symbol a lower layer offers to a higher one is exported, which is what the
  layered split does at every boundary it draws. Lower a symbol when its last
  external use disappears.

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
  the underlying error.** `erun-ui/internal/runtimeview/usage.go` and
  `erun-ui/internal/runtimeview/env_usage.go` both lead with `Cannot read this
  environment's resource usage: ` and let the cause follow;
  `erun-ui/internal/runtimeview/sizing.go` does the same for a sizing
  recommendation. A bare `exit status 1: kubectl ...` with no
  headline is the departure this names.
- **A message names the command actually invoked, or names none.**
  `erun-common/open.go`'s shared tenant resolver hardcodes its own caller --
  `open could not infer one` -- so `erun usage` and `erun observe` both report that
  `open` failed (#2363). A command that names itself must be the one the operator
  ran; where a shared helper genuinely cannot know its caller, it names no command.
  `erun-cli/cmd/pin/pin.go`'s `` see `erun pin --list` `` is the honoured form.
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

- After Go changes run `go tool golangci-lint run` and
  `go test -count=1 -race ./...`. The erun-devops image's `cli-test` stage runs both;
  race coverage is essential for leases, job supervisors, and workspace sync.
- While iterating, use plain `go build` / `go test -run` on the package you
  touched. `-race` and the darwin/windows cross-builds each compile a separate
  copy of this package into the Go build cache; leave them to `erun build`, whose
  `cli-test` stage runs both. Go evicts a cache entry only after 5 days unused and
  has no size cap, so the cache grows with every edit; `erun trim-cache` caps it.
- Exercise binary-reachable behavior through integration scenarios; remove unit
  tests duplicating those scenarios. Keep focused tests for otherwise unreachable
  behavior and concurrency. See `erun-integration/AGENTS.md` § "Known integration
  coverage gaps" for accepted limitations.
