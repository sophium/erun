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
- `fixture.SeedTenantEnv(t, setup, tenant, env)` writes the minimum erun config tree so commands resolve a tenant/environment without prompting. Extend this helper when a new field becomes load-bearing for `--dry-run`.
- `fixture.SeedDevopsRepo(t, setup, tenant)` materializes a `<tenant>-devops/k8s/<tenant>-devops/` chart so deploy/build resolve a kubernetes deploy context.
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
- Workflow for a new scenario:
  1. Decide what the scenario actually needs to assert. Most of the time it is one whole-output snapshot. Sometimes it is also a side-effect check from one of the cases above.
  2. Regenerate the snapshot file from a passing run.
  3. Read the generated file end to end. Look for anything that will differ between machines, runs, or developers: paths, generated identifiers, hashes, timestamps, sizes, ordering.
  4. Extend the normalization layer to cover any new source of nondeterminism. Regenerate.
  5. Re-run without regeneration to confirm the snapshot is stable.
  6. If the output refuses to stabilize and the scenario does not match one of the documented exceptions, the fix usually belongs in production code, not in the test: lift the missing trace, gate the side effect on the dry-run boundary, or surface the decision the test wants to assert. Do not work around an unstable trace with a substring-only assertion.

## Coverage gate

- `make integration-test` from the repo root: cleans `coverage/raw`, builds instrumented `erun`, runs `go test ./...` with `GOCOVERDIR` set, merges counters with `go tool covdata textfmt`, and fails non-zero if total statement coverage of `erun-cli` + `erun-common` falls below `COVERAGE_THRESHOLD` (default 90).
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

Some functions in `erun-cli/cmd` cannot be exercised from a CLI subprocess driven by `--dry-run` traces alone, even with stubs. They show as 0% in the integration coverage report and the gate carries them as a known shortfall rather than a regression:

- **IDE launchers** (`open_ide.go`): functions like `intelliJGatewayProjectURI`, `vscodeRemoteFolderURI`, and the `runJetBrainsBootstrapAttempt` helpers compute URIs and command lines that production code passes directly to `exec.Command` without first emitting them to the trace stream. Until production traces "would launch IDE with: …" before the spawn, dry-run cannot lock these paths and integration scenarios cannot diff their output.
- **TTY-gated alias setup** (`maybeConfigureOpenNoShellAlias`, `writeOpenNoShellHintLines`, `appendOpenNoShellAlias`): these only run when stdout is a TTY (`info.Mode() & os.ModeCharDevice != 0`). The integration runner pipes stdout into a buffer, so the stat check fails and the entire alias-prompt branch is unreachable from this suite.
- **Live shell loop** (`runShellLoop`, `traceShellPreview`): the loop calls back into `kubectl exec` and spawns interactive subprocesses; integration cannot drive the reattach/replace-pod cycle without a TTY.

The right fix for the IDE launchers is to lift the URI/command computation in front of a `ctx.TraceCommand(...)` call so dry-run mode can show what would be launched and stop there. Once that lands, the IDE branches become reachable from `--dry-run` and the matching scenarios can be added without stubs.

For the TTY-gated paths, the alternative is to expose a deterministic test hook that overrides the TTY check (e.g. a `ERUN_FORCE_TTY=1` env var read from the runner). Until that exists, those branches stay covered by same-package unit tests in `erun-cli/cmd/open_*_test.go`. AGENTS.md flags those unit tests as not contributing to the gate; treat them as documentation of expected behavior, not as coverage.

When you add a new branch to `open` (or any command), check first whether `--dry-run` can reach it — if not, lift the trace, do not extend the unit-test island.

## Anti-patterns

- Skipping a regression scenario to keep CI green. The whole point of the suite is that a green main means no known regressions.
- Asserting against `result.Stdout` only when the trace lines go to stderr. Use `result.Combined` unless you have a specific reason to separate them.
- Adding scenarios that depend on real cluster/cloud state. The harness deliberately has no live targets; if a scenario needs one, redesign it around stubs or `--dry-run`.
- Inlining stubs that branch on argv via shell `case`. If the stub is non-trivial, factor it into `internal/fixture/` so other scenarios can reuse it.
- Letting goldens drift unreviewed. A PR that touches `testdata/` must explain every changed file in its description.
