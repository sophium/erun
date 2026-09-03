# AGENTS.md

Repository guidance for the integration test module. Follow the parent `AGENTS.md` first; this file adds rules specific to `erun-integration/`.

## Purpose

`erun-integration/` runs the compiled `erun` binary as a subprocess against per-command goldens. It exists to catch the bug class that unit tests miss: production command-tree wiring (registration, flag attachment, help discovery), end-to-end `--dry-run` traces (audit lines, action sequences, decision lines), and cross-module behavior (CLI invoking common code with real configs). Coverage from this suite is the only signal the gate in the root `Makefile` enforces.

## Module shape

- Own `go.mod` (peer to `erun-cli` / `erun-common` / `erun-mcp`). Keeps test-only deps out of production module graphs.
- `go.work` includes `erun-cli`, `erun-common`, `erun-mcp` so the harness can build the binary against the same workspace any developer or CI uses.
- All test files live at the module root (one `<command>_test.go` per top-level erun command). No internal test helpers leak into production code.

## Harness layout

```
erun-integration/
├── go.mod / go.work / .gitignore
├── Makefile target lives at repo root; calls scripts/integration-test.sh
├── scripts/integration-test.sh   — build, run, merge coverage, gate
├── internal/
│   ├── erun/        — TestMain compiles instrumented binary; Run() invokes it
│   ├── env/         — fresh HOME/XDG_CONFIG_HOME/cwd per scenario
│   ├── fixture/     — seed configs + git repo + stub binaries
│   ├── normalize/   — strip paths/versions/timestamps from output
│   └── golden/      — read/write testdata; UPDATE_GOLDEN=1 to regenerate
├── <command>_test.go — Test<Command> with t.Run() per scenario
└── testdata/<command>/<scenario>.txt
```

## Scenario shape

A scenario is one `t.Run("name", func(t *testing.T) { ... })` inside `Test<Command>`:

```go
t.Run("name_of_scenario", func(t *testing.T) {
    setup := env.New(t)
    fixture.SeedTenantEnv(t, setup, "team", "dev")
    result := erun.Run(t, []string{"command", "args", "--flag", "--dry-run"}, erun.RunOptions{
        Cwd: setup.Cwd,
        Env: setup.Env(),
    })
    golden.Equal(t, "command/name_of_scenario", normalize.Apply(result.Combined))
})
```

- One scenario per flag combination that materially changes the resolved plan. Variants that don't change the plan (e.g., different tenant names with the same resolved spec) are not worth a separate scenario at all — they don't catch a different bug. Don't relocate them to unit tests; just leave them out.
- Always pass `--dry-run` unless the scenario is intentionally testing real-mode execution via stub binaries. The harness has no real cluster, real cloud, or live runtime to talk to.
- Capture `result.Combined` (stdout + stderr) for the golden. The audit line and the trace lines are both relevant; splitting them hides ordering bugs.
- Assert sparingly outside `golden.Equal`. Hard-coded substring checks (e.g., "expected `Deploy the current Helm chart` in help") are valuable for *regression markers* — they make a failing test self-explanatory — but the golden is the ground truth.

## --dry-run is a public contract

Per the root `AGENTS.md`, `--dry-run` must produce a complete, side-effect-free plan. The integration suite enforces this:

- Every action erun would take gets a trace line. External commands go through `RawCommandRunner` which short-circuits in dry-run after `ctx.TraceCommand`, so the user sees the exact command. Filesystem writes use the same pattern (`ctx.TraceCommand("", "write-yaml", path)`).
- Every decision that influences the plan gets a trace line. Examples: which kubernetes context was picked and why, which chart matched, snapshot mode on/off, which cloud-provider alias resolved.
- Bailouts must trace what was attempted before they exit. `release.go`'s `resolveReleaseInputs` is the reference: it traces "release: resolving X" before each step and "release: X failed: ..." on the way out, so the user sees where the plan stopped.
- Adding a new code path that mutates state without a corresponding trace line is a bug. Fix the trace before the integration scenario can land.

## Fixture patterns

- `env.New(t)` allocates a fresh tempdir-rooted HOME/XDG/cwd. Always call this; never share env across scenarios.
- `erun.Run` enforces the isolation: it fails the test (`t.Fatal`) when `RunOptions.Env` lacks an isolated `HOME` + `XDG_CONFIG_HOME` or points `HOME` at the developer's real home. Always build the env from `setup.Env()` (appending extra vars is fine); a scenario can never silently read or write the real `~/.config/erun`.
- `fixture.SeedTenantEnv(t, setup, tenant, env)` writes the minimum erun config tree so commands resolve a tenant/environment without prompting. Extend this helper when a new field becomes load-bearing for `--dry-run`.
- `fixture.SeedDevopsRepo(t, setup, tenant, environment)` materializes a `<tenant>-devops/k8s/<tenant>-devops/` chart so deploy/build resolve a kubernetes deploy context.
- `fixture.SeedGitRepo(t, dir)` runs `git init` + commit so release/diff/exec see a project root.
- For commands that prompt interactively, prefer flags that bypass prompts (`--confirm-environment`, `--set-default-tenant=true`, `-y`) over scripted stdin. Goldens are easier to read without prompt redrawing.

## The PATH is scrubbed: nothing ambient is reachable

`setup.Env()` sets `PATH` to one generated directory (`setup.PathDir`) and does **not** inherit the host's. No host-installed binary is reachable from a scenario unless the scenario declared it. This is the contract that keeps the gate a contributor runs and the gate that blocks a release from disagreeing: the runtime image's test stage runs `make check` with no kubectl, helm, docker, aws or gh, while every developer machine and the agent pod have all of them. Before the scrub, a scenario that reached for an ambient binary recorded that host's answer into its golden and went red only inside the image build — eleven minutes into `erun build`, after the release tag was already cut.

What the scrubbed PATH holds, both declared in `internal/env/env.go`:

- `shellUtilities` — forwarders for the POSIX utilities the suite's **own stub scripts** use (`cat`, `dirname`, `mkdir`, `sleep`, `touch`, `tr`, `wc`). A stub runs as a child of the binary under test and inherits its PATH, so without these a stub that keeps a counter file cannot run. Add a name here only for a utility a stub body needs; never for a tool erun itself invokes.
- `hostTools` — routed through their `ERUN_<NAME>_BIN` seam at an absolute path rather than through PATH. `git` only: the fixtures build real repositories with it and the release/diff/exec scenarios read real git state, so no stub can stand in for it. Treat any addition here as a new host prerequisite for the whole suite, and justify it.

Consequences for writing a scenario:

- A command that invokes an external binary needs a declared stub — `fixture.Stub*` plus `fixture.StubEnv` for anything reached through `eruncommon.Command`, or a stub directory prepended to PATH for anything reached through `exec.LookPath` (`gh`, `dpkg-deb`, the IDE launchers).
- When a scenario prepends its own PATH entry, append `setup.PathDir` after it (`"PATH="+stubs+string(os.PathListSeparator)+setup.PathDir`) rather than `os.Getenv("PATH")`, or that scenario alone goes back to seeing the whole host.
- `erun.Run` fails the test when the captured output shows `exec: "<name>": executable file not found`, naming the binary. That is the authoring-time signal: the scenario reached past its declared stubs.
- A scenario that wants a binary to be *absent* needs no override — it already is. Say so in the test comment so a reader knows the absence is the point.
- Nothing in the suite may gate on host capability. `runtime.GOOS` checks and `exec.LookPath` probes in test bodies were how host dependence used to hide: they turned into a `t.Skip` on some machines and a live tool on others, so a shared-fixture change could leave a golden stale and green everywhere except the image build. Pin the host through `ERUN_HOST_OS_OVERRIDE` and the tool through a stub instead — `TestRelease/dry_run_includes_linux_release_scripts` does both and runs everywhere as a result.

## Stubbing rules: dry-run-first, decision-input second

Integration scenarios run `erun` in `--dry-run` mode by default. Dry-run is meant to be fully auditable: every action and every decision must surface as a trace line, so a complete dry-run output is sufficient evidence that the command would behave correctly. Pile on stubs only when the dry-run trace already shows the action; never use stubs to *replace* a missing trace.

There are two legitimate uses of stub binaries in this suite, and one anti-pattern:

### Allowed: stubs as dry-run decision input

Some commands branch on the *output* of an external binary, not on its side effect. `erun open`, for instance, calls `kubectl get deployment` to decide whether to redeploy the runtime. Even with `--dry-run` the decision needs an answer: "does the deployment exist?" Without a stub, the command falls through to whatever `kubectl` happens to be installed and points at, the developer's machine drives the branch, and goldens drift between machines.

For these scenarios it is correct to stub the binary so its output is deterministic. The stub is decision input, not a side-effect replacement: the dry-run contract still applies (no real cluster mutation, no helm install), but the branch the runner picks is now reproducible.

Worked example — `open_test.go::no_shell_dry_run`:

```go
stubs := setup.Cwd + "/stubs"
fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
    Stderr:   `Error from server (NotFound): deployments.apps "team-devops" not found`,
    ExitCode: 1,
})
envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
result := erun.Run(t, []string{"open", "team", "dev", "--no-shell", "--no-alias-prompt", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
```

The stub returns "NotFound" so `CheckKubernetesDeployment` reports "not deployed" and the runner traces the redeploy branch. A second scenario can stub the same binary with `Stdout: "deployment.apps/team-devops"` + `ExitCode: 0` to reach the "already deployed, skip helm" branch. The two scenarios together lock both branches in goldens.

When you reach for this pattern, the test comment must say *which branch* the stub is unlocking and *why* the dry-run trace alone cannot reach it. If the answer is "the production code does not trace its decision," fix the production code first — see the next section.

### Allowed: stubs to drive non-dry-run side effects

A handful of scenarios run without `--dry-run` to exercise the real execution path: helm rollout waits, helm-recovery branches, retry loops, post-deploy kubectl polling. These cannot be reached from dry-run because their decisions depend on the side effect, not on a pre-action trace. `deploy_test.go::real_run_via_stubs` is the canonical example: it stubs `kubectl`, `helm`, and `docker` to return zero, then asserts on the user-facing `==> Deploying ... ==> Deployed in <ELAPSED>` lines that production code emits via `Info` (not `Trace`).

Real-run scenarios should be the minority. Add one only when:
- The behavior under test (e.g. spinner output, retry on auth error) is *only* visible in non-dry-run mode.
- The side effects can be fully captured by short-lived stub processes (no real network, no port binding the host might have in use).
- The scenario does not depend on the developer's filesystem layout, IDE installs, TTY state, or running ports.

### Not allowed: stubs that paper over a missing trace

If a code path cannot be reached from `--dry-run` because the production code does not trace its action, the defect is in the dry-run contract:

- Identify the missing trace. If a side effect happens without a `ctx.TraceCommand(...)` (or equivalent) call in front of it, add the trace.
- Gate the side effect with `if !ctx.DryRun { ... }` so the trace alone is enough to lock the behavior in the golden.
- Then write the integration scenario against the new trace output. No stub needed.

Reaching for a stub to "make this look real enough to reach the branch" is hiding a production bug. Resist it.

### Stubs must stay executable on Windows

A stub body is a POSIX shell script, and Windows can neither execute a shebang script by name nor preserve complex argv through a `.bat`. Two pieces of the fixture exist for that and must stay intact:

- `writeStub` drops a `<name>.exe` copy of `internal/fixture/stubrunner` beside the script, and `StubEnv`/`stubExecPath` route `ERUN_<NAME>_BIN` at the `.exe`. Production code that reaches a binary through `eruncommon.Command` therefore lands on the runner, which forwards argv byte-for-byte through a NUL-delimited file.
- The runner launches the script through the absolute shell in `ERUN_STUB_SH`, which `env.Env()` sets. It cannot look one up on PATH: the scenario PATH is scrubbed to `setup.PathDir`, which holds only forwarder scripts. A stub that silently produced nothing on Windows — leaving production to fall through its "no credential" arm and a scenario to assert an empty value — is what this routing prevents.

Goldens are byte-compared against LF output, so `erun-integration/.gitattributes` pins `testdata/**/*.txt` to `eol=lf`; a Windows checkout with `core.autocrlf=true` would otherwise fail every comparison on text that reads identically. Do not widen that entry to unrelated file types.

### Stub helpers

- `fixture.StubBinary(t, dir, name, stdout)` — stub that prints `stdout` and exits 0. Use for the non-dry-run real-run pattern when the call only needs to succeed.
- `fixture.StubBinaryAdvanced(t, dir, name, fixture.StubBinarySpec{Stdout, Stderr, ExitCode})` — full control over the stub's response. Use this for decision-input stubs that need a specific exit code or stderr message (e.g. `kubectl` reporting `NotFound`).
- `fixture.StubEnv(dir, names...)` — emits the `ERUN_<NAME>_BIN` env-var pairs that route production `Command(name, ...)` lookups to your stubs. Append to `setup.Env()` and pass via `erun.RunOptions.Env`.

`eruncommon.Command(name, args...)` honors `ERUN_<NAME>_BIN` so stubs work transparently for any production code path that uses `Command` to spawn external binaries.

## Platform-dependent goldens: pin via `ERUN_HOST_OS_OVERRIDE`

Some commands branch on host OS — IDE launchers (`open`/`xdg-open`/`cmd /c start`), JetBrains options-dir resolution, container-runtime socket candidates. A scenario that exercises one of those branches captures the host OS of the machine that ran `UPDATE_GOLDEN=1`, so the same golden is red on every other host.

Production code resolves the host OS through `eruncommon.DetectHost`, which honors the `ERUN_HOST_OS_OVERRIDE` env var (`darwin`/`linux`/`windows`). Use it to pin the scenario to a single canonical OS so the golden is deterministic everywhere:

```go
envVars := append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=darwin")
result := erun.Run(t, []string{"open", "team", "dev", "--vscode", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
golden.Equal(t, "open/vscode_dry_run", normalize.Apply(result.Combined))
```

Rules:

- Pick the host OS that best represents production usage (today most CLI flows are exercised on darwin) and set the override on every scenario whose output crosses an OS branch.
- If the same command needs coverage on more than one platform, add separate scenarios with separate goldens — `vscode_dry_run_darwin.txt` / `vscode_dry_run_linux.txt` — each one pinning a different `ERUN_HOST_OS_OVERRIDE`. Do not let "the test passes on someone's machine" stand in for explicit platform coverage.
- Document the override in the test comment so a future reader does not delete it. Without the override the scenario would silently re-shape the golden to whatever host ran `UPDATE_GOLDEN=1` last.
- The override is a deliberate test seam, not a production knob. If you find yourself wanting to set it from a CLI command or MCP tool, that is a sign the production code should be detecting differently — fix the detection.

### Do not gate a scenario on a real host capability

No scenario skips on host OS or on a tool being installed, and none may start doing so. `TestRelease/dry_run_includes_linux_release_scripts` used to need both `runtime.GOOS == "linux"` **and** `dpkg-deb` on PATH, so it skipped on macOS; it now pins `ERUN_HOST_OS_OVERRIDE=linux` and declares a `dpkg-deb` stub on PATH, and runs everywhere.

The trap that pattern created is why it is banned: `UPDATE_GOLDEN=1` on a host that skips the scenario leaves its golden un-regenerated, so a change to a **shared fixture** (e.g. `SeedReleaseRepo`) that alters its output goes green locally and red in the Linux image-build gate (`make check` inside the `erun-devops` Dockerfile) — the one place nobody is watching until `erun build` fails. A skipped scenario is not covered, and a scenario nobody notices is uncovered is worse than none.

The Linux container run is still the closest local stand-in for the image-build gate, and worth one pass when you touch a shared fixture or the harness itself:

```sh
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/tmp/gc -e GOMODCACHE=/tmp/gm \
  -v "$PWD":/src -w /src/erun-integration golang:1.26 sh -c \
  'git config --global --add safe.directory "*"; go test ./...'
```

## TTY-dependent branches: force via `ERUN_FORCE_TTY`

The alias-setup flow in `erun open --no-shell` only prompts when stdout is a
real terminal. The integration runner pipes stdout into a buffer, so the stat
check fails and the branch would be unreachable. Production resolves the check
through `stdoutIsTerminalForAliasSetup` (erun-cli/cmd/open.go), which honors
`ERUN_FORCE_TTY=1` so a scenario can opt into the TTY branch:

```go
envVars := append(setup.Env(), "ERUN_FORCE_TTY=1", "SHELL=/bin/zsh")
result := erun.Run(t, []string{"open", "team", "dev", "--no-shell"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars, Stdin: "y\n"})
```

The same rules as `ERUN_HOST_OS_OVERRIDE` apply: it is a deliberate test seam,
not a production knob; document the override in the test comment; if you want
to set it from a CLI command or MCP tool, the production detection is what
needs fixing.

## Scripted stdin into prompts: the plain (non-TTY) path

When stdout is a pipe — which it always is under this harness — erun's
prompts take the plain fallback (`runPlainPrompt`/`runPlainSelect` in
`erun-cli/cmd/plain_prompt.go`, #520): an fmt-rendered label plus a buffered
line read, no cursor-control escapes. Scenarios script prompts by passing
`erun.RunOptions.Stdin`, one line per prompt:

- A text prompt takes `"<text>\n"`; a bare `"\n"` submits the prompt's
  default.
- A select prints a numbered option list and takes `"<number>\n"` (or the
  exact option text). An empty line picks the first option, so a legacy
  `"\r"` confirm still selects it.
- The plain reader is shared across prompts, so a scenario may chain several
  prompts in one subprocess — feed one line per prompt in order.

The promptui branch (cursor repaints, `"j"`/`"k"` navigation) only runs on a
real terminal or under `ERUN_FORCE_TTY=1`; under the seam the old constraint
still applies: promptui's readline buffers ahead, so **at most one promptui
prompt per subprocess**. Verify a new prompt scenario with three consecutive
compare-mode runs before trusting it.

## Real-run port-forward scenarios: pin a high port range

Real-run `open` scenarios that start port-forward simulators persist
`localportrangestart: 26100` via `fixture.SeedRemoteTenantEnvWithSSHDPortRange`
so their ports (26100/26122/26133) never collide with a developer's live erun
session on the default 17000 range. Keep new real-run scenarios on that range
and keep `skipIfPortsBusy` as a last-resort guard only.

## Goldens and normalization

- A stub `httptest` server's port is assigned per run, so a scenario whose trace names the URL it called passes a per-server `normalize.Replacement` as an `Apply` extra rule (`exec_test.go`'s `stubServerRule`) instead of a blanket port rule in `internal/normalize`. A blanket rule was tried and rejected: it also collapsed the deliberately-pinned ports other scenarios assert (the 17000/26100 port-forward ranges), turning a real assertion into a token. Match the already-normalized `<LOOPBACK>` form — the default rules run before the extras.
- `golden.Equal(t, "command/scenario", normalize.Apply(out))` is the standard assertion. Normalization strips ANSI escapes, version numbers, ISO timestamps, compact timestamps, OS temp paths, hex tokens, and home-dir prefixes. Add new rules to `internal/normalize/` if a fresh source of nondeterminism appears in output.
- Set `UPDATE_GOLDEN=1` to (re)write the file when invoking `go test` directly (e.g. `UPDATE_GOLDEN=1 go test -run Test<Command> ./...`). Default mode is read-and-compare.
- The gate script (`scripts/integration-test.sh`, run by `make integration-test`/`make check`) never honors `UPDATE_GOLDEN` from the environment — it refuses outright if the variable is set, since Make exports command-line variables into recipe environments and an inherited `UPDATE_GOLDEN=1` would turn every `golden.Equal` into a silent write instead of a comparison while the gate still reports green. To reseed testdata, run `./erun-integration/scripts/integration-test.sh --update-golden` directly; it skips the coverage gate and cannot be triggered via `make check UPDATE_GOLDEN=1`.
- Goldens are reviewed artifacts. Treat a diff in any golden file as a behavior change to inspect, not as noise. If a trace line drift reflects an intentional change, update and explain in the PR. If it doesn't, the test is doing its job.

### Whole-output snapshots vs targeted substring assertions

- **Default to a whole-output snapshot assertion.** It locks the entire captured output, so any unrelated drift fails loudly with a complete diff. The diff is the failure message — you do not need a substring's "expected X" preamble.
- **Do not pair a substring assertion with a snapshot of the same stream.** When both run, the substring is redundant: if the snapshot passes, every substring within it passes; if the snapshot fails, the diff already shows the broken line. A redundant pair makes failures harder to read (two messages where one would do) and rots over time — when someone regenerates the snapshot but forgets the substring, the substring silently locks the old wording.
- A targeted substring (or shape) assertion is appropriate **only when the snapshot literally cannot make the assertion.** That is narrow, and tends to fall into one of these shapes:
  1. **The asserted value is masked by output normalization.** When the test's contract is "this specific dynamic value flowed through to the output" but the normalizer collapses every value of that shape to the same token, the snapshot cannot tell two valid values apart. A substring on the un-normalized capture is then the only way to assert the value the test actually cares about.
  2. **The assertion is on a side effect outside the captured streams.** Filesystem state, persisted config, recorded stub-server calls, parsed structured output — none of these are in the streams the snapshot covers. Use the appropriate read/inspection plus a shape check; this complements the snapshot, it does not replace it.
  3. **A specific line is intrinsically variable and cannot be normalized.** Output that genuinely depends on host clock, host platform, or other unstable inputs that no normalization rule can pin down. Match the variable line with a regex or pattern, accept that locking the whole stream is not possible, and document the reason in the test.
### Treat the snapshot as a specification, not just a string

The reason a whole-output snapshot is the right default is that the snapshot file becomes a readable, reviewable specification of what the command does. The diff in a PR is not just a regression signal — it is the way the command's behavior gets reviewed and remembered. That only works if the snapshot is honest about what the command would actually do.

Two failure modes to guard against:

- **A snapshot that locks output without locking intent.** A snapshot can stabilize on output that is technically deterministic but does not describe the command's contract — for example, a trace that goes silent before the real work, or a trace that names a side effect without naming the decision behind it. The test will pass forever, but a reviewer reading the snapshot cannot tell what the command is supposed to do, and the next person to change the production code can reshape behavior without the snapshot noticing.
- **A dry-run snapshot that diverges from real-run behavior.** The dry-run contract says the trace must show every action and every decision the command would take, in the same order, with the same inputs, so a developer can audit the plan before the real run. A snapshot whose dry-run trace omits an action that real-run performs, or shows an action real-run wouldn't take, is locking a lie. Adding more snapshots on top of a broken dry-run contract makes the lie permanent.

So a usable workflow is closer to:

  1. Decide what behavior the scenario is meant to pin down. Write that as a one-line description in the test comment so a future reader can compare the snapshot against the intent.
  2. Run in record mode and read the snapshot end to end against that description. Does it actually describe the command? Does each line correspond to a decision or action the command takes? Are the right things absent in dry-run, and the right things present?
  3. If the snapshot is missing the action or decision the test cares about, the production code probably isn't tracing it. Fix that first; do not reach for a substring assert to bridge the gap.
  4. If the dry-run snapshot says one thing and real-run does another, the dry-run contract is broken at that point. Fix the production code so dry-run faithfully reflects what real-run would do.
  5. Look for anything in the snapshot that will differ between machines, runs, or developers — paths, generated identifiers, hashes, timestamps, sizes, ordering — and add a normalization rule for each. Re-record.
  6. Run in compare mode and reproduce the snapshot exactly. Repeat on a clean state at least once before trusting it.
  7. In review, treat any snapshot diff as a behavior diff. Walk through the new content the same way you would walk through prose documentation: does it still describe what the command should do?

## Coverage gate

- `make integration-test` from the repo root: cleans `coverage/raw`, builds instrumented `erun`, runs `go test ./...` with `GOCOVERDIR` set, merges counters with `go tool covdata textfmt`, and fails non-zero if total statement coverage of `erun-cli` + `erun-common` falls below `COVERAGE_THRESHOLD` (the default is pinned in `scripts/integration-test.sh`; raise it in the same commit as the scenarios that earned the increase, keeping a small margin below the measured total).
- The threshold is a contract, not a target. Work that drops it must either restore coverage with new scenarios or open a discussion in the PR before lowering it.
- Coverage scope is set in `internal/erun.CoverPkgs`. Extending it to other modules requires both that constant and a corresponding gate update; do them in the same change.
- The gate measures statement coverage as reported by `go tool cover -func`. Function-touched rate is shown for diagnosis but not enforced.
- Integration scenarios are the only coverage signal the gate honors. Unit tests inside `erun-cli` or `erun-common` do not contribute to the gate, so any coverage they appear to provide is invisible at merge time and any overlap with an integration scenario is duplication.
- Therefore, write integration scenarios for `erun-cli` and `erun-common` behavior. If an existing unit test covers the same statements as an integration scenario, delete the unit test instead of carrying both.
- If a branch looks unreachable from the binary subprocess (e.g. an error message that never escapes a wrapper, a pure-parser branch with no production caller), the bug is almost always in the production path — not in the test strategy. Fix the production code so the branch becomes reachable from `--dry-run`, then write the integration scenario. Resist the urge to carve out a unit-test exception; leaving one is documentation that pushes the next reader to keep adding unit tests.
- When closing a coverage gap, extend an integration scenario. New flag combinations and richer fixtures are the lever; unit tests are not.

## Desktop-surface gate

`desktop_surface_test.go` (backed by `internal/desktopsurface/`) enforces root `AGENTS.md` § "Smooth, Seamless, No Dead Ends" failure mode 3: a capability the CLI, MCP, or the API can reach but no operator surface can is a dead end. It runs as an ordinary `go test` in this module, so it is part of `make integration-test`/`make check` with no extra wiring — the same precedent as every other gate in this file. Unlike the per-command golden tests, it is a repo-state structural check, not a `--dry-run` scenario, so it lives at the module root without a matching `testdata/` directory.

- `TestDesktopSurfaceGate` enumerates every capability from four sources — every registered MCP tool (`eruncommon.MCPToolNames()`), every CLI-only command with no MCP equivalent (walked from the real command tree via `cmd.CommandTreeForAudit()`), every registered HTTP route in `erun-backend-api/internal/routes` (parsed, not imported — that module is a separate Go module the same way `erun-ui` is, so the gate reads its `.go` source with `go/parser` rather than adding a cross-module dependency: every `register(http.MethodX, "path", ...)` call, the `routes.ProtectedRouteRegistrar` convention every route file uses, plus the couple of routes registered directly on the mux for `platform.go`/`invites.go`'s intentionally unauthenticated endpoints), and every entry in `eruncommon.OperatorSettableConfigFields` (`erun-common/operator_settable_config.go`) — a config field an operator is expected to set through some product surface rather than by hand-editing `config.yaml`. That fourth source is the one this gate cannot discover automatically: a struct field carries no "operator-settable" marker the way a Cobra command or an `MCPToolDescriptor` entry does, so the registry itself is the enumeration, hand-maintained the same way `cliOnlyAgentFacingCommands` is. It exists because `OrchestratorEnvConfig.Role` (erun#1745) shipped with a reader (`erun list`) and no writer for a full release, and none of the other three enumerations could see the gap — a bare config field is none of a route, an MCP tool, or a CLI command. Every source fails when a non-exempt entry has zero references in the operator surface: the concatenation of `erun-ui/frontend/src` (the desktop app) and `erun-console/src` (the hosted web console), the two committed, human-facing subtrees (their gitignored `wailsjs`/`dist`/`node_modules` siblings don't count, since a generated binding or bundle with no committed UI calling it is not a way in).
- CLI and MCP capabilities match by a case-insensitive plain substring `Token`, deliberately not word-bounded, so a capability named in a camelCase identifier (`WhipButton`) still counts — false positives here only ever make the gate too lenient (a real gap hiding behind a coincidental word match), never too strict, which is the direction that matters: a gate that blocks healthy work gets disabled. API routes match by a `Pattern` (a regular expression built by `desktopsurface.APIRoutePattern`) instead: a route's canonical path template (e.g. `/v1/users/{user_id}/roles`) requires every literal segment to appear, in order, with each `{param}` segment matching whatever a frontend call site interpolates in its place, bounded to that one path segment. A plain substring on the route's own last segment is not safe here the way it is for CLI/MCP tokens — `/v1/roles`'s and `/v1/users/{user_id}/roles`'s shared last segment, `roles`, is also an ordinary English word erun-ui/frontend already uses for an unrelated table column and `user.roles` display field, so a bare-word match would have wrongly cleared both routes. Matching the full path instead (`/v1/roles`, or the interpolation-tolerant pattern for the parameterized ones) is what tells the two apart.
- A route reachable only through the desktop can still fail the `Pattern` check even with a real, working UI behind it: `erun-ui/frontend/src` calls a Wails-bound Go method by name, never the route's own path — only `erun-ui/*.go` (calling `eruncommon.PlatformClient`) holds the literal path, and that Go source is outside what this gate reads. `Capability.WailsBinding` is the second, independent check this needs: a hand-verified `*App` method name, checked as a plain substring against the same frontend source `Pattern`/`Token` already read. It is a distinct field from `Token` rather than folded into it, so it cannot loosen the `Pattern`-over-`Token` precedent the "roles" collision above depends on. `desktop_surface_test.go`'s `apiRouteWailsBindings` table is the reviewable declaration site — each entry names, in its own comment, the `erun-ui/*.go` method and the `eruncommon.PlatformClient` call it makes, verified against real source on both ends before it is added, never guessed.
- A capability opts out of needing an operator entry point via an explicit, reviewable marker instead of a silent omission:
  - An MCP tool sets `AgentFacing: true` on its `eruncommon.MCPToolDescriptor` entry (`erun-common/mcp_tools.go`) — the same table every registered tool must already appear in.
  - A CLI-only command (no MCP tool) adds its space-joined path to `cliOnlyAgentFacingCommands` in `erun-cli/cmd/command_tree.go`.
  - A command that is Cobra `Hidden` or `Deprecated` needs no separate declaration — both are already explicit, reviewable markers at the command's own definition site, and `Hidden` propagates to every descendant (a leaf under a `Hidden` parent, e.g. `erun-cli/cmd/activity.go`'s whole family, is just as undiscoverable as the parent).
  - An API route adds its `"METHOD /path"` key to `InternalAPIRoutes` in `erun-backend-api/internal/routes/route_audit.go`, with a comment explaining why — the API-route twin of the two declarations above. Two entries today: `GET /v1/platform`, unauthenticated platform-discovery bootstrap a client fetches before it holds a token, the same infrastructure-only class `erun-backend-api/AGENTS.md`'s Authentication section already carves out for `/healthz`; and `POST /v1/reviews/{review_id}/builds`, which reports a build result the environment that ran the build already ran on its own — never something an operator clicks to trigger, unlike the rest of the merge-queue flow around it.
  - An operator-settable config field sets `Internal: true` (with `InternalReason` explaining why) on its `eruncommon.OperatorSettableConfigField` entry in `erun-common/operator_settable_config.go` — matching `InternalAPIRoutes`' discipline: silence must never be how a field opts out. No entry sets it today; every field in the registry currently expects an operator surface.
- Every enumerator supplies a `DeclarationHint` naming its own opt-out mechanism, so `Missing.Message()` points at the actual fix for that capability's source instead of a generic list of all four.
- `TestNoUnboundAppMethods` parses every non-test `.go` file directly under `erun-ui` (where `*App` lives) and flags a method on `*App` that is unexported *and* referenced by no other Go identifier in that file set: Wails only binds exported methods, so that combination is a binding location with no binding. `whipOrchestratorNow` shipped in exactly this shape — an unexported row-level whip written for a future UI action that never arrived, since the whip control that landed went global-only — and was deleted rather than exported once this gate caught it. Export the method or delete it if it is dead.
- Both classifiers are pure functions unit-tested directly in `internal/desktopsurface` against synthetic data (a declared-internal capability, an undeclared one, a camelCase match, a parameterized API route pattern matched and cleared, the "roles" word-collision regression above, an unbound method, a bound one). The wiring test (`desktop_surface_test.go`) supplies the real enumeration and real filesystem reads; keep new coverage of the *logic* in the classifier package, not by growing fixtures against the real repo tree.
- `TestNoUnboundAppMethods` is clean (whip's desktop control gave `whip` a real reference in `erun-ui/frontend/src`, and `whipAllOrchestratorsNow` is called from `WhipNow` in `erun-ui/whip.go`). `TestDesktopSurfaceGate` widened to see API routes found 33 with no reference in either `erun-ui/frontend/src` or `erun-console/src` — a real, pre-existing gap the gate was just taught to see, not a regression from teaching it: most of that surface (erun's own hosted reviews/build/merge-queue system, tenant/role/user administration beyond what `erun-console`'s identity panels already cover) has no console or desktop UI yet.

### Baseline for pre-existing gaps: `KnownUnsurfacedRoutes`

A gate that starts already red cannot be adopted without either gutting it (declaring every pre-existing gap "internal", most of which is false) or letting it warn instead of fail (which makes it not a gate). The standard way out is a baseline that can only shrink:

- `KnownUnsurfacedRoutes` in `erun-backend-api/internal/routes/route_audit.go` started at the 33 routes that had no reference anywhere the day the gate was widened. It is a **record of known gaps, not a design decision** — the opposite claim from `InternalAPIRoutes`, which asserts a route legitimately needs no surface forever. Do not confuse the two lists, and do not add a new route to `KnownUnsurfacedRoutes` to make a fresh gap disappear — it is not a second, softer opt-out.
- A `Capability` sets `KnownGap: true` (with a `BaselineHint`) when its key is in `KnownUnsurfacedRoutes`. `desktopsurface.FindMissingDesktopSurface` skips a `KnownGap` capability exactly like an `AgentFacing` one, which is what lets the widened gate land without fixing every route in the same change.
- The baseline is enforced shrink-only, not just documented that way: `desktopsurface.FindStaleBaselineEntries` walks every `KnownGap` capability and fails the gate if it now has a real frontend reference, naming the fix as "remove it from the baseline" rather than "add a surface" (`StaleBaselineEntry.Message()`, distinct from `Missing.Message()`). Landing a route's surface without deleting its `KnownUnsurfacedRoutes` entry in the same change is therefore a gate failure, not a missed cleanup — this is what stops the baseline rotting into a permanent amnesty nobody revisits.
- The baseline is down to 10 routes (of the original 33, plus `POST /v1/identity/orgs` added later as tenant registration grew an org-scoped-issuer path with no admin surface yet). Most of the original 33 turned out to already have a real desktop surface once `WailsBinding` let the gate see across the Wails IPC boundary (see above); the tenant quota pair (`GET /v1/quota`, `PUT /v1/tenants/{tenant_id}/quota`) gained a real console surface (`erun-console/src/quota/`, `erun-console/src/tenants/TenantQuotaDialog.tsx`); `POST /v1/reviews/{review_id}/builds` moved to `InternalAPIRoutes` (machine-reported, see above); and `GET /v1/invite-requests/mine` cleared the same way as the rest of the invite-requests family, via `WailsBinding` (`GetMyTenantInviteRequest`). What remains needs a surface too large for one change, not an exemption to look finished — see `KnownUnsurfacedRoutes`'s own comment in `route_audit.go` for the reasoning per family (erun's own hosted release view has no UI anywhere yet, unlike reviews/builds/merge-queue; tenant-issuer administration and the usage-event log need a designed admin surface, not a bare fetch; the DNS-01 token mint needs `erun expose` itself redesigned around it; org creation needs a designed identity-administration surface, not a bare button).
- Tracking issue: https://github.com/sophium/erun/issues/1497. `KnownUnsurfacedRoutes`'s own comment points here rather than carrying the issue number itself, per root `AGENTS.md` § "Code Comments"'s "no issue IDs in code comments" rule.

## Role-classification gate

`role_classification_test.go` (backed by `internal/roleclassification/`) enforces the same "classify every route or fail" discipline as the desktop-surface gate above, but for a different hazard: `erun-backend-api`'s `TenantUser`/`TenantAdmin` predefined roles are built from an exact, enumerated route list (`erun-backend-api/internal/routeroles`'s `Routes` map), not a wildcard pattern like `ReadAll`/`WriteAll`. A route added later that nobody classifies would silently fail to reach either narrower role — the opposite failure from the wildcard roles, which can never miss a new route because they match everything.

- `TestRouteRoleClassificationGate` parses two things with `go/parser`, the same "read the source, don't import it" approach the desktop-surface gate uses for the same cross-module reason: every `register(http.MethodX, "path", ...)` call site in `erun-backend-api/internal/routes` (the real, authenticated protected-route catalog — deliberately excluding the couple of routes `platform.go`/`invites.go` register directly on the mux, since an unauthenticated route never reaches `PermissionAuthorizer` and classifying it would assert something meaningless), and `routeroles`'s `Routes` map literal (reusing `apiRouteMapLiteral`, the same generic key-reader `InternalAPIRoutes`/`KnownUnsurfacedRoutes` use — it only needs a key's presence, not which `routeroles.Class` the value names). It fails on any registered route with no entry in the map.
- Unlike the desktop-surface gate, there is no baseline here: `routeroles.Routes` was authored as one complete pass over every route that existed the day `TenantUser`/`TenantAdmin` shipped, so the gate starts green rather than needing a shrink-only amnesty for pre-existing gaps.
- `internal/roleclassification`'s `Unclassified` is the pure classifier, unit-tested against synthetic route/classification pairs (a route present in the map, a route missing from it, several missing at once) so the underlying check is proven to actually fire independently of the real repo state the wiring test reads.
- Unlike `InternalAPIRoutes`/`KnownUnsurfacedRoutes` (bool-valued maps read only by this parser, never imported as Go code), `routeroles.Routes` also has a real Go consumer: `erun-backend-api/internal/repository` imports the `routeroles` package directly to derive `TenantUser`/`TenantAdmin`'s actual `role_permissions` grants from the same map this gate reads, so the classification and the grant can never drift apart into two separate lists.

## Build-check coverage gate

`build_check_coverage_test.go` enforces a different hazard from the two gates above: not "is this route/capability classified", but "does `make check` actually run this module's tests at all". erun-mcp shipped 282 passing test cases reachable by no gate for months — `LINT_MODULES` gave it golangci-lint, but a `go.work` `use` directive only resolves local module dependencies for the build; it does not make a sibling module's packages match another module's own `./...`, so neither `erun-cli`'s nor this module's `go test ./...` ever ran a single `erun-mcp` test. `erun-backend-api`'s ldflags regression and `erun-kit`'s/`erun-ui/frontend`'s own `yarn test` were the same class of gap before it, and `erun-devops/dns01-webhook` (a real regression suite protecting a deleted-token-secret cleanup fix, with no test stage of its own) turned out to be a fourth instance found only once this gate went looking.

- `TestBuildCheckGateCoversEveryTestSuite` enumerates every Go module (a directory holding a `go.mod`) and Yarn package (a `package.json` declaring a non-empty `"test"` script) in the checkout that has its own tests, walking the filesystem directly rather than importing anything — the same "read the source, don't import it" reasoning the other gates use, applied to the Makefile instead of Go source. A module with no tests of its own needs no entry.
- `buildCheckCoverage` is the classification map, modeled directly on `erun-backend-api`'s `tenant_scope_test.go`: every found module/package needs an entry naming either `gatedByMakeTarget` (with the Makefile target that runs it) or `deliberatelyExcluded` (with a reason). An exclusion has to name itself and say why — the same discipline `tenantScopeClassification`'s `deliberatelyCrossTenant`/`notTenantOwned` kinds enforce for repository methods.
- A `gatedByMakeTarget` claim is verified, not trusted: the gate parses the real `Makefile` text (a small hand-rolled `target: prereqs` / tab-indented-recipe reader, not a `make(1)` evaluator) to confirm the named target is actually a prerequisite of `check-gate`, and that the target's own recipe body references the module's directory. A target claimed but not wired into `check-gate`, or a target whose recipe doesn't actually touch the module, fails the gate exactly as if there were no entry at all — this is what makes the entry a fact about the Makefile rather than an assertion nobody checks.
- Stale entries fail too: an entry naming a module/package that no longer has tests (renamed, removed, or its tests deleted) is flagged, the same staleness check `TestContextOnlyRepositoryMethodsAreClassified` runs.
- `erun-cli` and `erun-common` are `deliberatelyExcluded`: both modules' `AGENTS.md` Validation sections record, as a considered design decision (not an oversight), that their behavior is gated end-to-end by this suite driving the compiled binary rather than by their own unit tests. `erun-ui/playwright` and `erun-console/playwright` are `deliberatelyExcluded` for the same reason the root `AGENTS.md` Makefile comment and their own `AGENTS.md` files already give: they need a built app (and, for the desktop suite, a real cluster) and run on their own schedule, never in the per-commit gate.

## Adding a new command's tests

1. Create `<command>_test.go` at the module root.
2. Add `Test<Command>` with subtests for each scenario: at minimum `--help`, plus one `--dry-run` per flag combination that changes the plan, plus error scenarios where the command fails informatively (missing tenant, missing chart, malformed flag).
3. `UPDATE_GOLDEN=1 go test -run Test<Command> ./...` to seed.
4. Run without the flag to confirm goldens diff cleanly.
5. Inspect the new goldens by hand. Does the trace cover every action and decision the command would take? If not, add the trace lines in production code, regenerate, repeat.

## Known integration coverage gaps

Some statements cannot be exercised from a CLI subprocess, even with stubs. They show below 100% in the coverage report and the gate carries them as a known shortfall rather than a regression. The historical headline gaps — IDE launchers, the TTY-gated alias setup, the shell loop, interactive prompts, port-forward workers, AWS error classifiers, config persistence — are all covered now (trace lifts, the `ERUN_FORCE_TTY` seam, scripted stdin, and real-run-via-stub scenarios). What honestly remains:

- **GitHub REST wire calls**: these had no seam at all until `ERUN_GITHUB_API_BASE_URL_OVERRIDE` (`erun-common/ghcr_push_preflight.go`'s `resolveGitHubAPIBaseURL`) was added — the same shape as the `runtimeregistry.baseurl` and `ERUN_UPGRADE_VERSIONS_OVERRIDE` seams, and a deliberate test seam rather than a production knob (nothing erun ships sets it). `exec_test.go`'s `TestExecRulesetBypass` uses it, with `platformAlias` for the gate-runs read, to drive `reconcile-bypass`'s and `plan-ruleset-bypass`'s real wire paths from the binary: list pagination via the `Link` header, per-suite detail, per-ruleset filtering (a suite whose bypass belongs to a *different* ruleset must drop out entirely), push-range expansion, tag lookup, gate-run cross-referencing, the unexpected-actor verdict, GitHub's own failure response surfacing, and each emitted ruleset payload asserted file-by-file. `erun-common/reconcile_bypass_test.go` was deleted rather than carried alongside those scenarios, per this file's own "delete the unit test instead of carrying both" rule. What is still covered only by `erun-common`'s own `httptest`-backed unit tests is `postGitHubCommitStatus` (`report_commit_status.go`) and `listOpenGitHubPullRequests`/`postGitHubIssueComment`/`closeGitHubPullRequest` + `ClosePullRequestHeadMovedError.Error` (`close_pull_request.go`) — the same seam now exists for them, so converting those to integration scenarios is available work rather than a structural gap. `COVERAGE_THRESHOLD`'s default moved from `75.6` to `75.3` in the commit that added `close_pull_request.go`'s network functions, and from `75.3` to `74.8` in the commit that added `reconcile_bypass.go`'s; the seam and its scenarios are what earned it back.
- **Live-network code with no seam**: `releaseArchiveSHA256`/`fetchReleaseArchiveSHA256` (and the checksum-sync callers that depend on them in real-run mode) GET a hardcoded `https://github.com/...` archive URL with plain `net/http`. The `erun version` registry lookup and the upgrade path both honor seams now — the root config's `runtimeregistry.baseurl`/`tokenurl` (scenarios point it at a local `httptest` server) and `ERUN_UPGRADE_VERSIONS_OVERRIDE` (`erun-common/upgrade.go`, used by `upgrade_test.go`) respectively — so the release-archive checksum fetch and the release's anonymous-pullability probe (`probeAnonymousManifestPull`/`probeAnonymousManifestPullAt`, `erun-common/release_anonymous_pullability.go`) are what remains seam-free. The probe's own discovery and per-image decision (`discoverModuleReferencedImageNames`, `verifyModuleReferencedImagesAnonymouslyPullable`, `verifyImageAnonymouslyPullable`) are gated on `!DryRun` only around the live call, so `release_test.go`'s `dry_run_main_traces_anonymous_pullability_probe_for_terraform_referenced_image` reaches everything up to that call from the binary; the real anonymous token exchange and manifest GET are exercised only by `erun-common`'s own `httptest`-backed unit tests (`erun-common/release_anonymous_pullability_test.go`), the same split `probeChartVersion` below uses.
- **Seam-shadowed live-network reads**: `probeChartVersion` (`erun-common/published_devops_chart.go`) probes the chart registry at each rung of the runtime chart search — the tenant's own `charts/<tenant>-devops`, then the shared `charts/erun-devops` in the same registry, then `charts/erun-devops` in the registry the runtime image comes from. Its live registry read is shadowed in the suite by the `ERUN_PUBLISHED_CHART_PROBE_OVERRIDE` decision-input seam. The default (`env.Env()`) marks only `erun-devops:*` published — the realistic baseline, since a real deploy only ever runs against an already-published version — while a tenant's own umbrella stays unpublished by default. An entry is `<chart>:<version>` (published in every registry) or `<registry>/<chart>:<version>` (published only in that one); `*` in place of the version marks a chart published at every version. The registry-qualified form is what lets a scenario put `erun-devops` in one registry and not another, which is the whole point of the search. `deploy_test.go` locks each rung: `dry_run_remote_env_prefers_tenant_published_runtime_chart` (rung 1, over an available rung 3), `dry_run_runtime_chart_prefers_the_deploy_registry_over_the_platform_registry`, `..._resolves_from_the_platform_registry`, and `dry_run_remote_env_refuses_when_no_runtime_chart_is_confirmed` for the nothing-confirmed refusal (#1193: a deploy that cannot confirm any candidate refuses rather than substituting the shared chart at a version it can never carry). The real network read itself therefore shows uncovered, in the same class as the `ERUN_UPGRADE_VERSIONS_OVERRIDE`-shadowed upgrade lookup.
- **Desktop- or MCP-only `erun-common` API**: functions whose only callers live in `erun-ui` or `erun-mcp` (`LogoutCloudProviderAlias`, `RuntimeDeployVersionSuggestions`, `BuildUpgradePlan`, `FindRootConfigBackupByDate`, `ResolveDoctorTarget`, `DescribeCloudContextStopProtection`, `LoadEnvironmentStopHistory`, `NormalizeDiffTarget`/`ResolveDiffTargetRoot`, `ParseRemoteAppSessionIDs`/`RemoteAppSessionEndScript`, `ContributeAppPortForResult`, `DesktopIdentityPublicKeyPath`, `LoadPortForwardState` (the CLI writes the state files; only the desktop reads them, to tell a reachable env from one nobody opened), the desktop deploy-checklist assembler `ResolveDeployableComponents` + its `publishablePlatformComponentNames`/`componentChartPrefix` helpers, the MCP build/push spec constructors `BuildExecutionSpecFromDockerBuilds`/`DockerPushExecutionSpecFromSpecs`, and the in-pod local outputs readers `ResolveLocalOutputs`/`StatLocalOutput`/`DownloadLocalOutput` + `resolveLocalOutputTarget`/`statLocalTarget`/`tarGzLocalDir`/`tarGzWriteEntry`) never run inside the instrumented `erun` binary. They are validated by their owning module's suites, not this gate. `LoginCloudProviderAlias`'s non-force token check and resolve-failure arm are in the same class: the CLI resolves the provider first and always calls with `Force=true`. Adding shared code in this class grows the coverage denominator without a reachable path, so it lowers the measured percentage; restore the margin with CLI `--dry-run`/real-run-stub scenarios rather than lowering the threshold (e.g. the GHCR `registry_auth.go` credential resolution is now exercised end-to-end from the binary by `version`'s docker-config scenarios via the `DOCKER_CONFIG` seam). `PlatformClient`'s invite-requests methods (`SubmitInviteRequest`, `MyInviteRequest`, `ListInviteRequests`, `ApproveInviteRequest`, `DeclineInviteRequest`, and `PlatformStatusError.RetryAfter`) join this list: the desktop's tenant dashboard, sidebar enrollment poll, and Requests tab call them (`erun-ui/tenant_platform_invite_requests.go`) and no CLI or MCP surface exists for the onboarding request/approve queue at all, by design — this feature is desktop- and console-only. (`SetInviteRequestRateLimit` was a sixth method here that no Go transport ever called — the console edits the rate limit with its own direct PATCH over RTK Query, never through this client — so it was dead code rather than desktop-only code, and has been deleted rather than kept as a false justification for this exemption.) `COVERAGE_THRESHOLD`'s default moved from `75.8` to `75.5` in the same commit that added these methods, the tracked discussion this section's own rule asks for, then back up to `75.6` once `SetInviteRequestRateLimit` (dead code, not desktop-only code) was deleted rather than kept as a false justification for the exemption.
- **Tooling-only `erun-cli/cmd` API**: `CommandTreeForAudit` and `IsAgentFacingCLIOnlyCommand` (`erun-cli/cmd/command_tree.go`) exist for the desktop-surface gate above to introspect the real command tree in-process; no code path inside the compiled `erun` binary itself calls them, so they show 0% here. Validated instead by the desktop-surface gate's own passing/failing behavior and by `internal/desktopsurface`'s unit tests.
- **The in-pod half of the whip environment push** (`erun-common/whip_environment.go`): `RunLocalEnvironmentWhip`, `environmentWhipSessionAlive`, `pushEnvironmentWhipNudge`, `loadWhipEnvironmentState`/`saveWhipEnvironmentState`, `latestEnvironmentAgentActivity`, `shellSingleQuote`, and the production `RunWhipCommand` runner have exactly one caller, `erun-mcp`'s `whip` tool (`whipTool` in `erun-mcp/whip.go`), which `resolveLocalTarget` restricts to acting on the server's own environment — structurally the same shape as the `Desktop- or MCP-only` bullet above, not a new exemption category. Unlike a desktop-only function, there is no equivalent CLI code path that could be pointed at instead: the dtach socket this pushes into only exists inside the target environment's own pod, so nothing outside an `emcp` process running there can reach it, in dry-run or otherwise. `erun-cli`'s own `whip` command (`erun-cli/cmd/whip.go`) calls out to that same tool over each environment's MCP edge and is fully exercised by `whip_test.go`'s goldens; what's uncovered here is only the tool's in-pod implementation. This is why `COVERAGE_THRESHOLD`'s default moved from `76.2` to `75.8` in the same commit that added this file — the tracked discussion this section's own rule asks for, rather than a silent drop.
- **The in-process task-job runner** (`erun-common/job_task.go`): `StartTaskEnvironmentJob`, `runTaskEnvironmentJob`, `runTaskEnvironmentJobBody`, and `TaskEnvironmentJobParams.normalize` have exactly one caller, `erun-mcp`'s job-envelope tools (`erun-mcp/job_envelope.go`) when a background action tool (`build`/`deploy`/`doctor`/...) is called with `wait: false` — structurally the same shape as the `Desktop- or MCP-only` and whip bullets above. There is no equivalent CLI code path: a task job is Go work run synchronously in the MCP server's own long-lived process rather than a subprocess a supervisor re-execs and waits on, so nothing outside an `emcp` process ever starts one, in dry-run or otherwise (`erun-cli` never calls `StartTaskEnvironmentJob` or constructs an `EnvironmentJobKindTask` record). `job_test.go`'s task-job scenarios (`a_job_that_ends_while_a_background_task_job_it_started_is_still_running_reads_as_gate_incomplete` and neighbors) exercise everything a CLI caller can reach about a task job — the parent's own gate-incomplete/startedJobFailed resolution, and `job status`/`job output` reading a task job's own record and log — by seeding the on-disk record the runner would have produced rather than by calling the runner itself, since only the runner's caller (`erun-mcp`) can do that. The runner's own body is validated by `erun-mcp`'s suite instead.
- **The erun cloud provider's Authorization Code + PKCE fallback**: `runERunAuthorizationCodeLogin`, `awaitERunOIDCCallback`, `erunCallbackHandler`, `exchangeERunAuthorizationCode`, `erunAuthorizationCodeURL`, and `erunPKCEVerifier`/`erunPKCEChallenge`/`erunPKCEState`/`erunRandomURLSafeString` (0%; `oauthTokenError` is the device grant's own error mapping too, so the device-flow scenarios' `authorization_pending` retry already covers it partially). The fallback runs only when the issuer's OIDC discovery advertises no `device_authorization_endpoint`; every scenario's stub issuer does, so the device grant (the flow that actually matters headlessly, e.g. from inside a pod) is what gets exercised instead. Reaching the fallback from this harness needs a concurrent actor to hit the printed loopback callback URL *while* the synchronous subprocess call blocks waiting on it — `erun.Run` has no such async/streaming mode (it starts the process, waits for exit, and only then returns output), unlike the in-process unit test (`TestRunERunAuthorizationCodeLoginRoundTrip` in `erun-common`) which can run the login in a goroutine and drive the callback from the same test process. Reaching this from the binary would need a harness change (an async `erun.Run` variant), not a scenario; until stage B needs it for another reason, this is an accepted gap rather than a lowered threshold.
- **`erunCloudProviderLogout`**: no CLI or MCP caller exists for any provider's logout today (`LogoutCloudProviderAlias` is desktop-only, see the bullet above) — the erun branch inherits the same exemption, not a new one.
- **Dead code with no callers in any module**: `resolveDeployContext` (its only
  call site passes an empty component name from desktop/MCP-only entrypoints; the
  non-empty arm is unreachable outright) and `publishablePlatformComponentNames`'s
  unreached arms. The rest of what this entry used to list — `DetectHostRuntime`,
  `DetectContainerRuntime`, `DetectKubernetesInstallation`,
  `ResolveDeploySpecForOpenResult`, `LaunchShell`, `DefaultEnvironmentIdleConfig`,
  `ValidateClaudeMaxOutputTokens`, `RuntimeVersionSuggestions`,
  `FindComponentLinuxPackageContext`, `ResolveCurrentLinuxReleaseScripts` — has
  since been deleted and no longer exists in the tree. `previousPatchVersion` does
  still exist and is **not** dead: `RuntimeDeployVersionSuggestions` calls it, and
  that is desktop-only, so it belongs to the class above. Verify a name here is
  still callerless before treating it as a deletion candidate.

- **Stat-based TTY checks with no override seam**: the spinner stack (`progress.go` `writerIsTerminalForSpinner` + `Spinner.run/draw/Stop`, `cmd/open_shell.go` `runWithSpinner`/`shouldUseSpinner`) and the log colorizer (`logger.go` `shouldColorizeWriter`/`colorize`) gate on `os.File` char-device stats, not on `ERUN_FORCE_TTY`. The piped harness can never reach them; adding the seam is a production change.
- **CLI-unreachable fallbacks**: `resolveDeployKubernetesContext`'s current-context fallback fires only for an env with an empty kubernetes context, which `validateOpenTarget` rejects earlier on every CLI path.
- **Second-sequential-prompt flows under `ERUN_FORCE_TTY`**: the plain (non-TTY) prompt path supports chained prompts (one stdin line each), but scenarios that force the promptui branch via `ERUN_FORCE_TTY=1` are still limited to one promptui prompt per subprocess, so TTY-branch coverage of chained prompts (doctor's multi-action confirm loop, `codeCommitSSHKeyIDPrompt` after the URL prompt) remains out of reach.
- **Host-OS-locked arms**: code gated on real `runtime.GOOS` rather than `eruncommon.DetectHost` (the darwin `.app` arm of `newAppProcessCommand`) only executes on that OS's runner.
- **Interactive-signal arms**: promptui `ErrInterrupt`/`ErrAbort` paths need Ctrl+C delivery on a real TTY.
- **Long-running loops with no clean exit**: `cmd/activity_proxy.go`'s accept loop blocks forever; only its validation arms are covered.
- **Defensive error arms** (chmod/marshal/stat failures and similar "should never happen" branches) that would need fault injection the harness does not do.

The `pin` and `sshd sync` families are covered end-to-end from the binary:
`pin_test.go` drives discovery against a local `httptest` registry, the
unpublished-target refusal, and a real apply → no-op → revert round trip with a
stubbed `helm`; `sshd_test.go` drives a real sync pass with a stubbed `ssh`,
which is what exercises the mirror's delete-and-prune lane rather than only its
resolution.

When you add a new branch to any command, check first whether `--dry-run` can reach it — if not, lift the trace into production code rather than adding a unit test in `erun-cli`/`erun-common`; those do not count toward the gate.

## Anti-patterns

- Skipping a regression scenario to keep CI green. The whole point of the suite is that a green main means no known regressions.
- Asserting against `result.Stdout` only when the trace lines go to stderr. Use `result.Combined` unless you have a specific reason to separate them.
- Adding scenarios that depend on real cluster/cloud state. The harness deliberately has no live targets; if a scenario needs one, redesign it around stubs or `--dry-run`.
- Inlining stubs that branch on argv via shell `case`. If the stub is non-trivial, factor it into `internal/fixture/` so other scenarios can reuse it.
- Letting goldens drift unreviewed. A PR that touches `testdata/` must explain every changed file in its description.
