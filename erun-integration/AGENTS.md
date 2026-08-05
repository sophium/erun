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

### Linux-only scenarios skip on macOS — regenerate their goldens in Linux

A few scenarios gate on a real Linux capability the override can't fake — notably `TestRelease/dry_run_includes_linux_release_scripts`, which needs both `runtime.GOOS == "linux"` **and** `dpkg-deb` on PATH (linux package builds), so it `t.Skip`s on macOS. `ERUN_HOST_OS_OVERRIDE` only fakes `DetectHost`; it does not conjure `dpkg-deb`, so these goldens can only be regenerated where the capability actually exists.

The trap: `UPDATE_GOLDEN=1` on macOS silently **skips** these scenarios, so a change to a **shared fixture** (e.g. `SeedReleaseRepo`) that alters their output leaves their goldens stale — green locally on macOS, red in the Linux image-build gate (`make check` inside the `erun-devops` Dockerfile), which is where it actually bites. When you touch a shared fixture, regenerate the linux-only goldens in Linux and run the full suite there before pushing:

```sh
docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/tmp/gc -e GOMODCACHE=/tmp/gm \
  -v "$PWD":/src -w /src/erun-integration golang:1.26 sh -c \
  'git config --global --add safe.directory "*"; UPDATE_GOLDEN=1 go test ./... && go test ./...'
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

- `golden.Equal(t, "command/scenario", normalize.Apply(out))` is the standard assertion. Normalization strips ANSI escapes, version numbers, ISO timestamps, compact timestamps, OS temp paths, hex tokens, and home-dir prefixes. Add new rules to `internal/normalize/` if a fresh source of nondeterminism appears in output.
- Set `UPDATE_GOLDEN=1` to (re)write the file. Default mode is read-and-compare.
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

## Adding a new command's tests

1. Create `<command>_test.go` at the module root.
2. Add `Test<Command>` with subtests for each scenario: at minimum `--help`, plus one `--dry-run` per flag combination that changes the plan, plus error scenarios where the command fails informatively (missing tenant, missing chart, malformed flag).
3. `UPDATE_GOLDEN=1 go test -run Test<Command> ./...` to seed.
4. Run without the flag to confirm goldens diff cleanly.
5. Inspect the new goldens by hand. Does the trace cover every action and decision the command would take? If not, add the trace lines in production code, regenerate, repeat.

## Known integration coverage gaps

Some statements cannot be exercised from a CLI subprocess, even with stubs. They show below 100% in the coverage report and the gate carries them as a known shortfall rather than a regression. The historical headline gaps — IDE launchers, the TTY-gated alias setup, the shell loop, interactive prompts, port-forward workers, AWS error classifiers, config persistence — are all covered now (trace lifts, the `ERUN_FORCE_TTY` seam, scripted stdin, and real-run-via-stub scenarios). What honestly remains:

- **Live-network code with no seam**: `releaseArchiveSHA256`/`fetchReleaseArchiveSHA256` (and the checksum-sync callers that depend on them in real-run mode) GET a hardcoded `https://github.com/...` archive URL with plain `net/http`. The `erun version` registry lookup and the upgrade path both honor seams now — the root config's `runtimeregistry.baseurl`/`tokenurl` (scenarios point it at a local `httptest` server) and `ERUN_UPGRADE_VERSIONS_OVERRIDE` (`erun-common/upgrade.go`, used by `upgrade_test.go`) respectively — so only the release-archive checksum fetch remains seam-free.
- **Seam-shadowed live-network reads**: `publishedChartHasVersion` (`erun-common/published_devops_chart.go`) probes the chart registry to decide whether `deploy` prefers a tenant's own `charts/<tenant>-devops` over the shared `charts/erun-devops`. Its live registry read is shadowed in the suite by the `ERUN_PUBLISHED_CHART_PROBE_OVERRIDE` decision-input seam — `env.Env()` sets it to an empty list globally so no scenario ever reaches a real registry and drifts a deploy golden by whatever charts happen to be published, and `deploy_test.go::dry_run_remote_env_prefers_tenant_published_runtime_chart` sets it to `team-devops:<version>` to lock the tenant-preferred branch (the sibling `dry_run_remote_env_uses_published_chart` locks the fallback). The real network read itself therefore shows uncovered, in the same class as the `ERUN_UPGRADE_VERSIONS_OVERRIDE`-shadowed upgrade lookup.
- **Desktop- or MCP-only `erun-common` API**: functions whose only callers live in `erun-ui` or `erun-mcp` (`LogoutCloudProviderAlias`, `ExportCloudProviderCredentials` + `parseAWSExportCredentials` + their default runners, `RuntimeDeployVersionSuggestions`, `BuildUpgradePlan`, `FindRootConfigBackupByDate`, `ResolveDoctorTarget`, `DescribeCloudContextStopProtection`, `LoadEnvironmentStopHistory`, `NormalizeDiffTarget`/`ResolveDiffTargetRoot`, `ParseRemoteAppSessionIDs`/`RemoteAppSessionEndScript`, `ContributeAppPortForResult`, `DesktopIdentityPublicKeyPath`, the desktop deploy-checklist assembler `ResolveDeployableComponents` + its `publishablePlatformComponentNames`/`componentChartPrefix` helpers, the MCP build/push spec constructors `BuildExecutionSpecFromDockerBuilds`/`DockerPushExecutionSpecFromSpecs`, and the in-pod local outputs readers `ResolveLocalOutputs`/`StatLocalOutput`/`DownloadLocalOutput` + `resolveLocalOutputTarget`/`statLocalTarget`/`tarGzLocalDir`/`tarGzWriteEntry`) never run inside the instrumented `erun` binary. They are validated by their owning module's suites, not this gate. `LoginCloudProviderAlias`'s non-force token check and resolve-failure arm are in the same class: the CLI resolves the provider first and always calls with `Force=true`. Adding shared code in this class grows the coverage denominator without a reachable path, so it lowers the measured percentage; restore the margin with CLI `--dry-run`/real-run-stub scenarios rather than lowering the threshold (e.g. the GHCR `registry_auth.go` credential resolution is now exercised end-to-end from the binary by `version`'s docker-config scenarios via the `DOCKER_CONFIG` seam).
- **Dead code with no callers in any module**: the container-runtime/k3s detection cluster in `host_runtime.go` (`DetectHostRuntime`, `DetectContainerRuntime`, `DetectKubernetesInstallation`, and the K3s/socket consts — but NOT `DetectHost`/`HostOS`, which `cmd/open.go` uses), `resolveDeployContext` (its only call site passes an empty component name from desktop/MCP-only entrypoints; the non-empty arm is unreachable outright), `ResolveDeploySpecForOpenResult`, `LaunchShell`, `DefaultEnvironmentIdleConfig`, `ValidateClaudeMaxOutputTokens`, `RuntimeVersionSuggestions` + `previousPatchVersion`, `FindComponentLinuxPackageContext`/`componentLinuxPackageContextCandidate`, and `ResolveCurrentLinuxReleaseScripts`. Candidates for deletion, not for scenarios.
- **Stat-based TTY checks with no override seam**: the spinner stack (`progress.go` `writerIsTerminalForSpinner` + `Spinner.run/draw/Stop`, `cmd/open_shell.go` `runWithSpinner`/`shouldUseSpinner`) and the log colorizer (`logger.go` `shouldColorizeWriter`/`colorize`) gate on `os.File` char-device stats, not on `ERUN_FORCE_TTY`. The piped harness can never reach them; adding the seam is a production change.
- **CLI-unreachable fallbacks**: `resolveDeployKubernetesContext`'s current-context fallback fires only for an env with an empty kubernetes context, which `validateOpenTarget` rejects earlier on every CLI path.
- **Second-sequential-prompt flows under `ERUN_FORCE_TTY`**: the plain (non-TTY) prompt path supports chained prompts (one stdin line each), but scenarios that force the promptui branch via `ERUN_FORCE_TTY=1` are still limited to one promptui prompt per subprocess, so TTY-branch coverage of chained prompts (doctor's multi-action confirm loop, `codeCommitSSHKeyIDPrompt` after the URL prompt) remains out of reach.
- **Host-OS-locked arms**: code gated on real `runtime.GOOS` rather than `eruncommon.DetectHost` (the darwin `.app` arm of `newAppProcessCommand`) only executes on that OS's runner.
- **Interactive-signal arms**: promptui `ErrInterrupt`/`ErrAbort` paths need Ctrl+C delivery on a real TTY.
- **Long-running loops with no clean exit**: `cmd/activity_proxy.go`'s accept loop blocks forever; only its validation arms are covered.
- **Defensive error arms** (chmod/marshal/stat failures and similar "should never happen" branches) that would need fault injection the harness does not do.

When you add a new branch to any command, check first whether `--dry-run` can reach it — if not, lift the trace into production code rather than adding a unit test in `erun-cli`/`erun-common`; those do not count toward the gate.

## Anti-patterns

- Skipping a regression scenario to keep CI green. The whole point of the suite is that a green main means no known regressions.
- Asserting against `result.Stdout` only when the trace lines go to stderr. Use `result.Combined` unless you have a specific reason to separate them.
- Adding scenarios that depend on real cluster/cloud state. The harness deliberately has no live targets; if a scenario needs one, redesign it around stubs or `--dry-run`.
- Inlining stubs that branch on argv via shell `case`. If the stub is non-trivial, factor it into `internal/fixture/` so other scenarios can reuse it.
- Letting goldens drift unreviewed. A PR that touches `testdata/` must explain every changed file in its description.
