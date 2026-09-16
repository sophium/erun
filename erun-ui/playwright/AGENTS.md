# AGENTS.md

Module-specific guidance for `erun-ui/playwright`. Follow the repository root `AGENTS.md` and `erun-ui/AGENTS.md` first, then apply this file for work in this subtree.

## Module Role

- `erun-ui/playwright` is a separate Yarn project that runs end-to-end UI tests for the desktop frontend.
- Tests drive `erun-app --headless` over the HTTP+SSE bridge instead of opening a Wails window. The same React bundle the desktop renders is served at `http://127.0.0.1:34123/`; method calls go through `/__erun_invoke`, events stream from `/__erun_events`, and `window.runtime` / `window.go.main.App` are shimmed at the document root.
- The backend runs against an isolated, suite-owned config root with a deterministic seeded baseline — never against the developer's real `~/.erun` / `~/.config/erun`. See "Isolated config root and seeded baseline" below.
- Use this suite for cross-component flows that depend on rendered DOM and round-trip backend calls — sidebar toggles, dialog interactions, layout panels, status banners, activity drawer state. It does not replace `go test ./...`: Go tests cover backend logic, Playwright covers the React frontend behaviour after a real boot sequence.

## Isolated config root and seeded baseline

The suite owns its config root. `fixtures/seedRoot.ts` owns layout, names, and seeding:

- `run.sh` creates a throwaway root (`mktemp -d …/erun-playwright-home.XXXXXX`), exports it as `ERUN_PLAYWRIGHT_HOME`, and removes it again via an EXIT trap. When `playwright test` is invoked directly, `playwright.config.ts` creates the root itself at config-load time.
- Default: one backend and isolated root per worker (`fixtures/workerBackend.ts`), beneath `ERUN_PLAYWRIGHT_HOME`, on base-port + parallel index. Seed at worker setup; global teardown removes the parent as a cleanup backstop. Do not add a shared `webServer` in this mode.
- k3d mode is the explicit exception: one worker, real cluster, shared backend and flat root. Worker fixtures reuse that backend instead of spawning another.
- Baseline: tenant `pw`, inert local-agent envs `alpha`/`beta`, alias `pw-aws`, and `aitool: sh`. Keep config fields aligned with integration fixtures; never launch real AI tools from inert tests.
- `backendEnv()` in `fixtures/seedRoot.ts` also sets two determinism seams on the backend process, both consumed in `erun-ui/app.go`/`erun-ui/session.go` and set nowhere in production:
  - `ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE=0` prevents real host listeners from masquerading as seeded environments.
  - `ERUN_LOCAL_SHELL_OVERRIDE=1` selects an rc-free shell. Wait for exported `LOCAL_SHELL_PROMPT` before screen-position assertions; keep it aligned with the backend prompt.
- Specs import the seeded names (`SEED_TENANT`, `SEED_ENV_ALPHA`, `SEED_ENV_BETA`) from `fixtures/seedRoot.ts` and assert against them directly. Do not query the sidebar to "pick the first available row" — the baseline is deterministic, so a missing seeded row is a bug the spec should surface.
- Mutating specs use the unique `seededEnv` fixture, wait for fsnotify visibility, and remove their env on teardown. Session/tab/lease churn must not touch baseline rows.
- Specs that need state the baseline does not carry should stage exactly what they need (extend the seed, write a per-test env, or stub the RPC over `/__erun_invoke`) instead of skipping. Reserve `test.skip` for state that genuinely requires a live cluster or cloud host (a stopped EC2 context, a real runtime pod, a real Codex session); name the constraint in a comment.

## Headless Launch

There is only one supported way to run the suite. The shell script `run.sh` in this directory is the single entry point — `yarn` scripts call it for convenience, and the desktop build/packaging flow invokes it too.

- `run.sh` is wired through `scripts/agent-gate.sh`, the same wrapper `make check` uses: outside an agent pod it behaves exactly as documented below, but inside one it detaches the whole run — build, lint, and the suite itself — through erun's own job primitive and awaits it for a bounded window, so this suite (longer than `make check`) never sits as an ordinary foreground command for an agent's harness to auto-background. A timeout says to run the same `run.sh` invocation again to keep waiting.
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
- Each Playwright worker has its own headless backend and its own isolated root (`fixtures/workerBackend.ts`); `playwright.config.ts` sets `fullyParallel: true` with a worker count picked from measured per-worker memory, not `nproc` (see the config's own comment for the current number and how it was measured). Each test gets a fresh browser context (fresh Redux state), and backend-side sessions persist across specs **within the same worker** — prefer the `seededEnv` fixture for specs whose tab/session churn would otherwise leak into the shared baseline rows. A spec that binds a real host port must derive it from something worker-unique, or pin itself to a single spec file (Playwright never splits one file across workers) so it can never collide with a concurrent worker's own real listener.
- The app fixture resets baseline Activity/Usage caches before boot. This does not reset sessions, tabs, or leases; those still require a per-test environment.
- **No flaky tests.** A spec that passes only sometimes is a defect, not noise — make it deterministic before the PR lands. Never paper over a flake by skipping it, marking it `test.fixme`/`test.skip`, relying on retries to eventually go green, or waving it through as "pre-existing" or "host-environment-flaky". Determinism comes from waiting on observable conditions, never on wall-clock time:
  - Use auto-retrying assertions — `await expect(locator).toBeVisible()` / `.toContainText(...)` / `.toBeEnabled()` / `expect.poll(() => ...)` — which retry up to the config's `expect` timeout. Do **not** lower a wait below that default (a `waitFor({ timeout: 3_000 })` that the rest of the suite would clear at 10s is the flake); let the auto-retry absorb a loaded machine.
  - Tie "give it a beat" to a real event, not a sleep: `await page.waitForResponse(...)` for an RPC round-trip (e.g. the next idle poll), or wait for the UI state the event produces. Never use `page.waitForTimeout(...)` — `playwright/no-wait-for-timeout` flags it for this reason.
  - "Assert nothing happened" (no extra RPC, no state flip) is the one hard case: wait for a deterministic completion signal first — a settled UI state, or the next `waitForResponse` — then assert, so the window is bounded by a real event rather than a guessed delay.
- Treat assertion failures as bugs to investigate. A red that is a real frontend regression → fix the frontend; a red that is non-deterministic → fix the determinism per above. When a red appears, rebuild from `main` and re-run the focused spec to learn whether your change caused it — that is a diagnostic to locate the cause, **not** licence to leave a confirmed flake in place. `test.skip` is reserved only for state the headless harness genuinely cannot reach (a live cluster, a real runtime pod, a real Codex session); stage everything else (see "Isolated config root and seeded baseline").
- Diagnose failures from report/trace timing and RPC responses before blaming resources. A shifting failed spec can indicate earlier shared-state corruption; `workerIndex` counts worker replacements, not concurrency.
- Config fixtures and the app are both writers. Read/parse the current YAML shape when modifying it; do not append/splice based on the original seed's indentation or key order.
- Hover cards can disappear on rerender. Use the sidebar hover helpers, keep observations in one re-drivable `toPass` block, minimize round trips, and use bounded `captureHoverCard` rather than an unbounded screenshot.

## Opt-in k3d e2e mode (issue #647)

The default suite is inert and offline: it PATH-prepends stub `kubectl`/`helm`/`docker` and pins `ERUN_APP_CLI` to an inert `erun` stub, so no real deploy ever runs. That is by design (no hosted CI, #521) but it structurally cannot see the desktop's full create → build → push → deploy → open → MCP flow — the bug class #644 fixed. The **opt-in k3d mode** exercises that flow against a real local cluster.

- **How to run:** `./run.sh --e2e-k3d` (sets `ERUN_E2E_K3D=1`). It builds the real `erun` CLI for the desktop tabs, registers binfmt for the mandatory multi-arch build, brings a throwaway k3d cluster + built-in registry up in `global-setup`, runs **only** `tests/e2e/`, and tears the cluster down in `global-teardown` (the `run.sh` EXIT trap is the backstop).
- **Host preconditions:** running Docker, k3d, and binfmt for the default multi-architecture build. This live mode is separate from the default suite and integration gate.
- **Gating:** the e2e specs live under `tests/e2e/` and are excluded from the default run via `playwright.config.ts` `testIgnore` (not per-spec `test.skip`), so the default suite never collects them. `ERUN_E2E_K3D=1` flips the un-stubbed `backendEnv()` branch (real tools + real `erun`, only `aws` stubbed via `ERUN_AWS_BIN`) and includes the dir. Keep both directions intact: the k3d branch must never leak into the default inert mode, and the inert specs (which assume stubs) must never run against the real-tool backend.
- **Determinism still binding (#643):** cluster specs are the classic flake source. Wait on observable conditions (activity-queue trace lines, the rendered ERun tab, pod-Ready), never wall-clock; size per-spec `test.setTimeout` for the real build → push → deploy round-trip (minutes), which is far slower than any default spec.

## Area-scoped gate selection

The merge gate selects Playwright areas from the test diff; this is selection, not permission to omit regression specs:

- **Specs are organized by area, not flat.** `tests/smoke/` holds one spec file covering every area's shallowest critical path — it always runs, on every build, regardless of selection. `tests/areas/<area>/` holds each area's full spec set. Area membership is directory position; nothing else declares it.
- **Selection follows changed specs, not a source-to-area glob map.**
  - A change that adds or edits spec files in `tests/areas/<area>/` → `erun build` runs **smoke + that area** (smoke plus every area whose specs changed, for a multi-area diff).
  - A change that touches no spec file at all (source-only) → `erun build` runs **smoke only**.
  - A change under `tests/fixtures/`, `tests/pages/`, `global-setup.ts`, `global-teardown.ts`, or `playwright.config.ts` affects every area at once → `erun build` runs **everything** (smoke + every area).
  - The full suite always remains runnable on demand — `./run.sh` with no `PLAYWRIGHT_TEST_AREAS` set, `erun e2e`'s post-deploy backstop (#2092), a nightly run, or a developer iterating locally. This changes what the _gate_ selects, not what exists.
- **Fail-safe direction: an unresolvable selection runs everything, never zero.** `resolvePlaywrightTestAreaSelection` (`erun-common/build_playwright_areas.go`) returns "could not resolve" when there is no git repository or no merge base against any candidate upstream branch, and the caller leaves `PLAYWRIGHT_TEST_AREAS` unset in that case — the Dockerfile's own `ARG PLAYWRIGHT_TEST_AREAS=""` default, which `run.sh` reads as "run everything". A gap in the mapping costs time, never coverage.
- **Mechanism:** resolve selection where git metadata exists, then pass `PLAYWRIGHT_TEST_AREAS` through the Docker build argument and `make check` environment into `run.sh`. Empty/unresolved means the full suite, never zero.
- **`erun e2e` (#2092) is the backstop**, not a replacement for this gate: it runs the full suite post-deploy against the real deployment, catching a cross-area regression this change-scoped selection missed (a shared-state change that breaks an area the diff didn't touch). Landing that backstop is what makes it safe to stop running everything pre-merge.

### Area taxonomy

`tests/areas/<area>/` is the canonical membership list, not a copied file-count
table. Reuse the appropriate existing area; when adding/reassigning areas, update
smoke coverage and selection tests in the same PR. Smoke must cover each area's
critical path.

- For changes to the desktop create → deploy → open flow or its deployed artifacts, also run the opt-in k3d e2e mode (`./run.sh --e2e-k3d`) on a Docker + k3d + binfmt host — it is the only signal that exercises the real runtime end-to-end.
- After failures, run `yarn report` to open the HTML report. Traces and screenshots for failed tests live under `playwright-report/` and `test-results/`.
