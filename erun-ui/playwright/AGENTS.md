# AGENTS.md

Module-specific guidance for `erun-ui/playwright`. Follow the repository root `AGENTS.md` and `erun-ui/AGENTS.md` first, then apply this file for work in this subtree.

## Module Role

- `erun-ui/playwright` is a separate Yarn project that runs end-to-end UI tests for the desktop frontend.
- Tests drive `erun-app --headless` over the HTTP+SSE bridge instead of opening a Wails window. The same React bundle the desktop renders is served at `http://127.0.0.1:34123/`; method calls go through `/__erun_invoke`, events stream from `/__erun_events`, and `window.runtime` / `window.go.main.App` are shimmed at the document root.
- The backend runs against an isolated, suite-owned config root with a deterministic seeded baseline — never against the developer's real `~/.erun` / `~/.config/erun`. See "Isolated config root and seeded baseline" below.
- Use this suite for cross-component flows that depend on rendered DOM and round-trip backend calls — sidebar toggles, dialog interactions, layout panels, status banners, activity drawer state. It does not replace `go test ./...`: Go tests cover backend logic, Playwright covers the React frontend behaviour after a real boot sequence.

## Isolated config root and seeded baseline

The suite owns its config root (issue #483). `fixtures/seedRoot.ts` is the single owner of the layout, the seeded names, and the helpers; the moving parts are:

- `run.sh` creates a throwaway root (`mktemp -d …/erun-playwright-home.XXXXXX`), exports it as `ERUN_PLAYWRIGHT_HOME`, and removes it again via an EXIT trap. When `playwright test` is invoked directly, `playwright.config.ts` creates the root itself at config-load time.
- `playwright.config.ts` points the `webServer` (`erun-app --headless`) at the root by setting `HOME`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, and `XDG_DATA_HOME` (the same redirect seam `erun-integration/internal/env.New` uses — config root is `xdg.ConfigHome + "erun"`, runtime state is `os.UserHomeDir() + ".erun"`, so HOME+XDG redirect both). It also prepends a stub dir to `PATH` so `kubectl`/`helm`/`docker`/`aws` are inert for the backend and for every `erun`/shell child it spawns, and pins `reuseExistingServer: false` so a stale dev server pointed at another root can never be reused.
- `global-setup.ts` creates the layout (`home/`, `home/.config/`, `home/.cache/`, `home/.local/share/`, `repo/`, `stubs/`) and seeds the baseline before the backend boots; `global-teardown.ts` removes the root.
- The seeded baseline is one tenant `pw` with two inert local-agent envs `alpha` and `beta`, plus one configured cloud provider alias `pw-aws` (backed by the aws stub). Env configs mirror `erun-integration/internal/fixture.SeedTenantEnv`'s tree — keep the two in lockstep when a config field becomes load-bearing — plus `type: local-agent` (the explicit-type badge contract) and `aitool: sh` (the AI tab launches an inert shell, never a real claude/codex).
- `backendEnv()` in `fixtures/seedRoot.ts` also sets two determinism seams on the backend process, both consumed in `erun-ui/app.go`/`erun-ui/session.go` and set nowhere in production:
  - `ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE=0` pins `canConnectLocalPort`/`canReachMCPEndpoint` to "unreachable". Without it, the seeded envs' computed local port ranges can coincide with a real port genuinely bound on the host (this host's own MCP/SSH forwards, or another agent's), and an occupancy/idle-status check would read that real listener as if it belonged to the seeded, never-deployed env.
  - `ERUN_LOCAL_SHELL_OVERRIDE=1` pins the Local tab's shell to a real, rc-free POSIX shell with the fixed prompt exported as `LOCAL_SHELL_PROMPT` (`'erun-test$ '`, kept in lockstep with `session.go`'s `localShellDeterministicPrompt`) instead of the operator's `$SHELL`. A spec that selects Local-tab terminal text by screen position must wait for `LOCAL_SHELL_PROMPT` to appear first — otherwise a real shell's dotfile-configured, possibly live-redrawing prompt can race the spec's own write and land in the row the spec reads.
- Specs import the seeded names (`SEED_TENANT`, `SEED_ENV_ALPHA`, `SEED_ENV_BETA`) from `fixtures/seedRoot.ts` and assert against them directly. Do not query the sidebar to "pick the first available row" — the baseline is deterministic, so a missing seeded row is a bug the spec should surface.
- Specs that mutate per-env state (open/close churn, extra terminals, status injection) use the `seededEnv` fixture in `fixtures/erunApp.ts`: it provisions a uniquely-named inert env (`<spec-slug>-<rand>` under `pw`) by writing the same config tree, waits for the backend's fsnotify watcher to surface the row, and removes the env on teardown. Created envs are inert local-agent envs — never deployed — so setup and teardown need no cluster or cloud.
- Specs that need state the baseline does not carry should stage exactly what they need (extend the seed, write a per-test env, or stub the RPC over `/__erun_invoke`) instead of skipping. Reserve `test.skip` for state that genuinely requires a live cluster or cloud host (a stopped EC2 context, a real runtime pod, a real Codex session); name the constraint in a comment.

## Headless Launch

There is only one supported way to run the suite. The shell script `run.sh` in this directory is the single entry point — `yarn` scripts call it for convenience, and the desktop build/packaging flow invokes it too.

- `run.sh` is wired through `scripts/agent-gate.sh`, the same wrapper `make check` uses (root `AGENTS.md` § "Long Gates Detach Themselves Inside An Agent Pod"): outside an agent pod it behaves exactly as documented below, but inside one it detaches the whole run — build, lint, and the suite itself — through erun's own job primitive and awaits it for a bounded window, so this suite (longer than `make check`) never sits as an ordinary foreground command for an agent's harness to auto-background. A timeout says to run the same `run.sh` invocation again to keep waiting.
- One-shot from this directory:
  ```sh
  ./run.sh
  ```
  Defaults: headless browser, port `34123`. Uses the existing `../bin/erun-app` if present; builds it only when missing. Packaging pipelines that produced the binary in an earlier step skip the build cost.
- Equivalent through Yarn (every script delegates to `run.sh`):
  ```sh
  yarn test         # default headless
  yarn test:headed  # visible browser, same backend
  yarn test:ui      # Playwright interactive runner
  yarn test:debug   # pause-on-step
  yarn test:rebuild # force `../bin/erun-app` to be rebuilt first
  ```
- One-time setup (idempotent; `run.sh` runs these automatically when needed):
  ```sh
  yarn install
  yarn install-browsers
  ```

`run.sh` flags:

- `--build` force a desktop-binary rebuild even when `../bin/erun-app` exists. Use this after editing Go code.
- `--skip-build` deprecated no-op kept for older callers; the default behaviour already avoids building when the binary is present.
- `--skip-lint` skip typecheck/lint/format:check for this invocation only, forwarding the same skip to `build.sh` when a rebuild runs. Per-invocation only — it cannot arrive from an environment variable, and a skipped run always prints `>> SKIPPING ...` so the skip is never silent. Use only when iterating locally; never in CI.
- `--port N` override the backend port. Defaults to `34123` to avoid clashing with `wails dev`'s `34115`. Exported as `ERUN_PLAYWRIGHT_PORT` so `playwright.config.ts` stays in sync.
- `--headed` run the browser with a visible window. Otherwise headless.
- `--` everything after this is forwarded to `playwright test` (e.g. `./run.sh -- --grep sidebar`).
- Any unrecognised flag is also forwarded to `playwright test`, so `yarn test --grep sidebar` works even though Yarn 1 strips its own `--` separator before reaching the script.

`run.sh` is the canonical entry point from desktop build/packaging flows — `build.sh`-style automation should call `erun-ui/playwright/run.sh` rather than chaining the underlying `yarn` and `playwright` commands by hand. Packaging pipelines that produce `bin/erun-app` themselves can call `./run.sh` directly; the script will reuse the binary.

## Frontend And Backend Split

- Page object classes go in `pages/`. Each file owns one component surface (sidebar, titlebar, a single dialog, a single panel) and exposes high-level actions rather than raw locators or selectors.
- Tests in `tests/` consume POMs through the fixtures in `fixtures/erunApp.ts` (`test`, `expect` re-exports, plus the per-test `seededEnv` env). Avoid calling `page.click(...)` or `page.locator(...)` directly from specs.
- Keep specs deterministic by asserting against the seeded baseline names from `fixtures/seedRoot.ts` (or a `seededEnv` row). The backend boots against the suite-owned isolated root, so the rows are the same on every machine; do not re-introduce "query the sidebar and pick the first available row" discovery.

## Selector Conventions

- Prefer accessible queries: `getByRole`, `getByLabel`, `getByText`. Match the `aria-label` / `aria-labelledby` values the production React components already set.
- When two surfaces share a role (`tablist`, `dialog`), scope the locator to the parent component's POM — e.g. `ManageDialog.getActiveTab()` queries inside the manage dialog locator, not at the document level.
- If a target has no accessible label, fix the component first (add `aria-label`), then write the test. Reaching for `data-testid` is acceptable only when no semantic equivalent exists.

## Working Rules

- Boot races are real. The fixture's `AppShell.open()` waits for the "Loading environments..." overlay to clear before yielding control. New specs that mount their own page state should rely on the fixture rather than calling `page.goto` directly.
- The Cancel buttons on `Init` and `Manage` dialogs sit below the default 900 px viewport. The config uses `1440x1200`; keep tall-dialog tests on that viewport rather than shrinking it.
- The headless backend is a singleton; `playwright.config.ts` sets `fullyParallel: false` and `workers: 1`. Keep it that way unless you split the backend into per-test processes. Each test gets a fresh browser context (fresh Redux state), but backend-side sessions persist across specs — prefer the `seededEnv` fixture for specs whose tab/session churn would otherwise leak into the shared baseline rows.
- **No flaky tests.** A spec that passes only sometimes is a defect, not noise — make it deterministic before the PR lands. Never paper over a flake by skipping it, marking it `test.fixme`/`test.skip`, relying on retries to eventually go green, or waving it through as "pre-existing" or "host-environment-flaky". Determinism comes from waiting on observable conditions, never on wall-clock time:
  - Use auto-retrying assertions — `await expect(locator).toBeVisible()` / `.toContainText(...)` / `.toBeEnabled()` / `expect.poll(() => ...)` — which retry up to the config's `expect` timeout. Do **not** lower a wait below that default (a `waitFor({ timeout: 3_000 })` that the rest of the suite would clear at 10s is the flake); let the auto-retry absorb a loaded machine.
  - Tie "give it a beat" to a real event, not a sleep: `await page.waitForResponse(...)` for an RPC round-trip (e.g. the next idle poll), or wait for the UI state the event produces. Never use `page.waitForTimeout(...)` — `playwright/no-wait-for-timeout` flags it for this reason.
  - "Assert nothing happened" (no extra RPC, no state flip) is the one hard case: wait for a deterministic completion signal first — a settled UI state, or the next `waitForResponse` — then assert, so the window is bounded by a real event rather than a guessed delay.
- Treat assertion failures as bugs to investigate. A red that is a real frontend regression → fix the frontend; a red that is non-deterministic → fix the determinism per above. When a red appears, rebuild from `main` and re-run the focused spec to learn whether your change caused it — that is a diagnostic to locate the cause, **not** licence to leave a confirmed flake in place. `test.skip` is reserved only for state the headless harness genuinely cannot reach (a live cluster, a real runtime pod, a real Codex session); stage everything else (see "Isolated config root and seeded baseline").

## Opt-in k3d e2e mode (issue #647)

The default suite is inert and offline: it PATH-prepends stub `kubectl`/`helm`/`docker` and pins `ERUN_APP_CLI` to an inert `erun` stub, so no real deploy ever runs. That is by design (no hosted CI, #521) but it structurally cannot see the desktop's full create → build → push → deploy → open → MCP flow — the bug class #644 fixed. The **opt-in k3d mode** exercises that flow against a real local cluster.

- **How to run:** `./run.sh --e2e-k3d` (sets `ERUN_E2E_K3D=1`). It builds the real `erun` CLI for the desktop tabs, registers binfmt for the mandatory multi-arch build, brings a throwaway k3d cluster + built-in registry up in `global-setup`, runs **only** `tests/e2e/`, and tears the cluster down in `global-teardown` (the `run.sh` EXIT trap is the backstop).
- **Host preconditions:** Docker running, `k3d` installed, and binfmt registered for the foreign arch (`erun` always builds `linux/amd64` + `linux/arm64`; the #645 daemon-capability preflight fails fast otherwise). Without these the mode cannot run — it is a developer/manual gate, never part of the default `run.sh` or `make integration-test`.
- **Gating:** the e2e specs live under `tests/e2e/` and are excluded from the default run via `playwright.config.ts` `testIgnore` (not per-spec `test.skip`), so the default suite never collects them. `ERUN_E2E_K3D=1` flips the un-stubbed `backendEnv()` branch (real tools + real `erun`, only `aws` stubbed via `ERUN_AWS_BIN`) and includes the dir. Keep both directions intact: the k3d branch must never leak into the default inert mode, and the inert specs (which assume stubs) must never run against the real-tool backend.
- **Determinism still binding (#643):** cluster specs are the classic flake source. Wait on observable conditions (activity-queue trace lines, the rendered ERun tab, pod-Ready), never wall-clock; size per-spec `test.setTimeout` for the real build → push → deploy round-trip (minutes), which is far slower than any default spec.

## Validation

- Run `./run.sh` (or `yarn test`) before pushing changes that touch any frontend component, slice, thunk, or controller method exposed to the React tree.
- For changes to the desktop create → deploy → open flow or its deployed artifacts, also run the opt-in k3d e2e mode (`./run.sh --e2e-k3d`) on a Docker + k3d + binfmt host — it is the only signal that exercises the real runtime end-to-end.
- After failures, run `yarn report` to open the HTML report. Traces and screenshots for failed tests live under `playwright-report/` and `test-results/`.
