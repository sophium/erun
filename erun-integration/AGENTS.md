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

- One scenario per flag combination that materially changes the resolved plan. Variants of the same plan (e.g., different tenant names) belong in unit tests, not here.
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

## Stub-binary injection

Production code goes through `eruncommon.Command(name, args...)`, which honors `ERUN_<NAME>_BIN` (e.g., `ERUN_KUBECTL_BIN=/path/to/stub`). This unlocks scenarios that need to walk the post-trace branches that `--dry-run` short-circuits past:

```go
stubs := setup.Cwd + "/stubs"
fixture.StubBinary(t, stubs, "kubectl", "")
fixture.StubBinary(t, stubs, "helm", "")
envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
```

Rules:

- Stubs return canned output via `echo`. Make the output match what the real tool would emit closely enough to drive the next branch — empty output is fine if the production code only checks exit status.
- Reach for stubs only when `--dry-run` cannot reach the code path. If the path is gated by `if !ctx.DryRun { ... }`, the stub-driven scenario is the only way; otherwise add a richer `--dry-run` fixture instead.
- Each `exec.Command` call site in production code must go through `eruncommon.Command(...)` so it picks up the override. Direct `exec.Command(...)` is forbidden in production paths — the harness can't intercept it.

## Goldens and normalization

- `golden.Equal(t, "command/scenario", normalize.Apply(out))` is the standard assertion. Normalization strips ANSI escapes, version numbers, ISO timestamps, compact timestamps, OS temp paths, hex tokens, and home-dir prefixes. Add new rules to `internal/normalize/` if a fresh source of nondeterminism appears in output.
- Set `UPDATE_GOLDEN=1` to (re)write the file. Default mode is read-and-compare.
- Goldens are reviewed artifacts. Treat a diff in any golden file as a behavior change to inspect, not as noise. If a trace line drift reflects an intentional change, update and explain in the PR. If it doesn't, the test is doing its job.

## Coverage gate

- `make integration-test` from the repo root: cleans `coverage/raw`, builds instrumented `erun`, runs `go test ./...` with `GOCOVERDIR` set, merges counters with `go tool covdata textfmt`, and fails non-zero if total statement coverage of `erun-cli` + `erun-common` falls below `COVERAGE_THRESHOLD` (default 90).
- The threshold is a contract, not a target. Work that drops it must either restore coverage with new scenarios or open a discussion in the PR before lowering it.
- Coverage scope is set in `internal/erun.CoverPkgs`. Extending it to other modules requires both that constant and a corresponding gate update; do them in the same change.
- The gate measures statement coverage as reported by `go tool cover -func`. Function-touched rate is shown for diagnosis but not enforced.

## Adding a new command's tests

1. Create `<command>_test.go` at the module root.
2. Add `Test<Command>` with subtests for each scenario: at minimum `--help`, plus one `--dry-run` per flag combination that changes the plan, plus error scenarios where the command fails informatively (missing tenant, missing chart, malformed flag).
3. `UPDATE_GOLDEN=1 go test -run Test<Command> ./...` to seed.
4. Run without the flag to confirm goldens diff cleanly.
5. Inspect the new goldens by hand. Does the trace cover every action and decision the command would take? If not, add the trace lines in production code, regenerate, repeat.

## Anti-patterns

- Skipping a regression scenario to keep CI green. The whole point of the suite is that a green main means no known regressions.
- Asserting against `result.Stdout` only when the trace lines go to stderr. Use `result.Combined` unless you have a specific reason to separate them.
- Adding scenarios that depend on real cluster/cloud state. The harness deliberately has no live targets; if a scenario needs one, redesign it around stubs or `--dry-run`.
- Inlining stubs that branch on argv via shell `case`. If the stub is non-trivial, factor it into `internal/fixture/` so other scenarios can reuse it.
- Letting goldens drift unreviewed. A PR that touches `testdata/` must explain every changed file in its description.
