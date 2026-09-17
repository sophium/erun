# AGENTS.md

Follow root guidance. This module owns compiled-CLI golden scenarios and
cross-repository structural gates, not production helpers.

## Purpose and module shape

- Keep the harness in its own Go module/workspace so test dependencies do not
  leak into production. Command scenarios live at module root, goldens under
  `testdata/<command>/<scenario>.txt`, reusable helpers under `internal/`.
- `internal/erun` builds/runs the instrumented binary; `env` isolates scenarios;
  `fixture` seeds state/stubs; `normalize` removes nondeterminism; `golden`
  compares reviewed output. Read these implementations instead of copying examples.
- Only subprocess coverage contributes to this gate's CLI/common percentage.
  Owner unit suites may validate genuinely non-CLI behavior; they do not contribute
  to that percentage.

## Scenario shape and dry-run contract

- Add help, meaningful plan-changing flag combinations, and informative error
  cases for each command. Different values producing the same plan do not merit
  duplicate tests.
- Default to `--dry-run` and compare normalized `result.Combined`, preserving
  stdout/stderr ordering. State each scenario's intended behavior in its comment.
- Every action, decision, and bailout must be visible in the trace, with the same
  resolved inputs/order as real execution and no side effects. Fix a missing trace
  in production rather than hiding it behind a stub.
- Stubs are appropriate for dry-run decision inputs or fully isolated real-run
  behavior that depends on effects (rollout, recovery, prompts). Explain the branch
  they unlock. Never contact a real cloud/cluster or use ambient operator state.
- Factor non-trivial argv-dependent stubs into `internal/fixture`, not inline
  shell programs copied between scenarios.

## Isolation and portability

- Every scenario starts with `env.New(t)` and passes `setup.Env()` to the runner.
  HOME, XDG config, cwd, and mutable fixtures must be scenario-private. The runner
  refuses missing isolation or the operator's real home.
- PATH is deliberately scrubbed. Declare every production tool through fixture
  stubs and `ERUN_<NAME>_BIN`, or explicit stub PATH entries for LookPath callers.
  Append `setup.PathDir`, never host PATH. Missing binaries are authoring failures;
  when absence is the test's point, say so.
- `shellUtilities` exists only for tools used inside stub scripts.
  `hostTools` uses absolute seams for real fixture prerequisites (currently git);
  an addition creates a suite-wide prerequisite and needs justification.
- Use shared tenant/devops/git fixture seeders; extend them when new fields matter.
  Prefer explicit prompt-bypass inputs unless the prompt itself is under test.
- Pin OS-dependent branches with `ERUN_HOST_OS_OVERRIDE`, using separate goldens
  for separate platforms. Never skip because of host OS/tool availability.
  This and `ERUN_FORCE_TTY` are test seams, not production configuration knobs.
- On Windows, preserve the executable stub runner, NUL-delimited argv transfer,
  and absolute `ERUN_STUB_SH` (the scrubbed PATH cannot discover a shell).
  Keep golden files LF via the narrowly scoped `.gitattributes` entry.
- For shared fixture/harness changes, validate on Linux as well as the local host;
  local success must not depend on tools absent from the image test stage.

## Prompts, ports, and parallelism

- Plain piped prompts share one buffered reader: supply one stdin line per prompt,
  including blank lines for defaults and numbers/text for selections.
  Forced promptui/TTY cases support only one prompt per subprocess because of
  readline lookahead; compare a new prompt scenario three times before trusting it.
- Default to `t.Parallel()` for isolated scenarios and eligible top-level tests.
  Fresh `httptest` servers with OS-assigned ports are safe; fixed-port fixture
  data alone is not a concurrency hazard.
- Prefer ephemeral real listeners for new work. Legacy fixed-port scenarios use
  the fixture's high port range and busy-port guard; neither the scenario nor its
  top-level test may be parallel. Treat a busy-port skip as missing coverage,
  not a clean verdict, and do not add host-dependent skips to new scenarios.
- Size test concurrency with `parallel-gate.sh`'s CPU-quota-aware width, not raw
  host core count/GOMAXPROCS. Real supervisors judged by heartbeat deadlines can
  falsely appear dead under starvation; observable polling alone cannot fix a
  starved producer. Keep their current parallel execution, but investigate real
  failures through resource evidence and width/heartbeat calibration, not retries
  until green or reflexive whole-suite serialization.

## Goldens and normalization

- Whole-output snapshots are the default specification. Do not pair redundant
  substring assertions with the same snapshot. Extra assertions are justified
  for values normalization masks, external side effects/structured data, or a
  genuinely unnormalizable line with an explained limitation.
- Normalize incidental paths, times, IDs, and ordering without erasing the contract.
  Dynamic server ports use per-server extra rules matching already-normalized
  `<LOOPBACK>`; do not blanket-normalize meaningful fixed ports.
- Record through direct `UPDATE_GOLDEN=1 go test ...` or the script's explicit
  `--update-golden` mode, then read every changed golden against intended behavior
  and compare on clean state. A golden diff is a behavior diff, not generated noise.
- The gate script refuses inherited `UPDATE_GOLDEN`; explicit regeneration skips
  coverage enforcement and is never a passing compare-mode gate.
- Explain changed testdata in review. If dry-run and real-run disagree, repair
  the production trace/effect boundary before recording a new expectation.

## Coverage gate

- Root `make integration-test` drives `scripts/integration-test.sh`: fresh raw
  counters, instrumented CLI run, merged CLI/common statement coverage, then the
  script-owned threshold. Keep `CoverPkgs` and enforcement aligned when scope changes.
- Restore coverage with meaningful CLI scenarios; do not lower thresholds to
  accommodate a change. Function-touched rate is diagnostic, not the enforced metric.
- Prefer integration coverage for CLI-reachable behavior and remove equivalent
  white-box duplication. Do not invent public code paths merely to reach genuinely
  transport-specific or defensive internals; use the owning suite for those.
- Re-measure cited baselines cleanly and without contention, verify zero skips,
  and compare per-file uncovered statements. Matching totals or repeated successful
  exit codes alone do not establish a regression or complete coverage.

## Desktop-surface gate

- `desktop_surface_test.go` enumerates registered MCP tools, the real CLI tree,
  parsed API routes, and the manually maintained `OperatorSettableConfigFields`
  registry. Check both committed desktop and console sources, excluding generated
  bindings/bundles. New operator-settable fields need registration.
- CLI/MCP matches use tokens; API matches use complete interpolation-aware path
  patterns, not the final path word. Desktop-only API routes may declare a
  `WailsBinding` only after verifying both the frontend call and real Go/client
  route linkage.
- Exemptions must be explicit and justified at their owner: MCP `AgentFacing`,
  CLI-only declaration or inherited Hidden/Deprecated marker, API `InternalAPIRoutes`,
  config `Internal` plus `InternalReason`. Supply source-specific declaration hints.
- `TestNoUnboundAppMethods` detects unexported, otherwise-unreferenced App methods.
  Bind/export intended surface methods or remove dead code.
- Test classifier logic against synthetic inputs in `internal/desktopsurface`;
  keep real-repository enumeration in the wiring tests. Token presence is only a
  structural lower bound, not proof of usable UI; shared UX review still applies.

### Baseline for pre-existing gaps: KnownUnsurfacedRoutes

This is a shrink-only list of real gaps, not an internal-only exemption. Do not
add fresh omissions to it; remove an entry in the same change that surfaces it.
Stale entries fail. Read the current list and family-specific reasons in
`erun-backend-api/internal/routes/route_audit.go`, rather than preserving a count
here. Remaining administration/release surfaces need designed workflows, not
bare fetches that satisfy the matcher. Tracking: erun#1497.

## Role-classification gate

Every protected route must be classified in `internal/routeroles.Routes`; there
is no baseline. The API derives actual TenantUser/TenantAdmin grants from that
same map. Parse the separate module's route source rather than importing it;
exclude intentionally unauthenticated mux routes. Test classification logic
with synthetic inputs independently of the repository wiring.

## Build-check coverage gate

- Discover Go modules with tests and Yarn packages with test scripts. Every suite
  needs a `buildCheckCoverage` entry naming a real Make target or a reasoned exclusion.
- Verify target reachability from `check-gate`, a recipe referencing the actual
  module, and absence of stale classifications. Workspace membership and lint
  participation do not imply a sibling's tests run.
- Common's unit suite is wired through `test-erun-common`; CLI behavior uses
  this integration suite. Desktop Playwright is wired through `test-playwright`.
  Console's real-stack suites remain explicit,
  separate runs. Windows cross-compilation does not prove native desktop behavior.

## Doc-drift gate

- Cross-check a high-risk, mechanically comparable claim against its owning code
  source and one designated public spec. The infrastructure signature check uses
  `GateRunInconclusiveSignatures()` and `agent-reference/skills-spec.md`, tolerating
  prose changes via distinctive nouns. It does not prove all related pages agree
  or that nuanced prose is correct; those need a manual sweep.
- Keep chart-value assertions beside the chart renderer (e.g. retention controls),
  not a duplicate rendering mechanism here. Do not build a generic prose-truth checker.
- All structural tests reading external files must run with `go test -count=1`.
  Add each new external input to the image test stage's COPY set in the same change.

## CLI help drift gate

- Check command/flag existence against the actual command tree, including inherited
  flags and the end-of-flags marker. Parse exact literal `erun` invocations from
  examples and backticked prose without interpreting argument values as new invocations.
- Reconstruct context-sensitive top-level build/push commands from their own
  constructors when cwd omits them. Container subcommands are not interchangeable:
  top-level push has additional flags.
- Gate-run status vocabulary is a separate code-owned check, including backend
  case normalization at each input entry point. Extend this pattern only for
  other specifically justified vocabularies.
- These checks do not establish useful prose, effective sentinel defaults, or all
  enum semantics. Human help review belongs to `erun-cli/AGENTS.md`.
  External-source reads require `-count=1` and matching Docker COPY inputs.

## Known integration coverage gaps

Verify callers and current scenarios before treating a historical gap as still open.

- Live release-archive checksums and anonymous registry probes lack full subprocess
  wire seams; published-chart/upgrade network reads may be shadowed by decision
  overrides. Preserve owning HTTP-level tests and exercise every reachable decision.
- GitHub status/PR helpers have a wire seam available; remaining unit-only coverage
  is conversion work, not a structural exemption. Ruleset bypass/reconciliation
  already has real binary wire scenarios.
- Desktop/MCP-only common APIs, in-pod whip, and in-process MCP task jobs cannot be
  started by the CLI just to increase coverage. Test their owning transports;
  CLI scenarios can still validate persisted job records and parent outcomes.
  Audit-only command-tree helpers are covered by structural tests, not subprocess counters.
- Authorization-code/PKCE callback fallback needs an asynchronous harness actor;
  the synchronous runner exercises device flow instead. Existing in-process
  round-trip tests cover the fallback pending that harness capability.
- Deployment-settings comparison needs a stub distinguishing name and JSON reads.
  Cached-deploy runtime-version healing needs realistic build/push cache scaffolding;
  retain its owner regression until a scenario reaches that actual decision.
- True terminal stats/color/spinners, second forced-promptui prompts, real-OS-only
  arms, interactive signals, endless accept loops, and defensive filesystem/marshal
  errors need additional harness/fault-injection support. A test seam is a deliberate
  code change, not an excuse to weaken an assertion.
- Callerless branches are removal candidates only after verifying all modules.
  Do not mislabel a desktop-only caller as dead or preserve inventories of deleted code.

Guidance-only changes follow root's consistency/reference validation scope; they
do not require a new golden baseline or a full instrumented run.
