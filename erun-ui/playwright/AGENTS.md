# AGENTS.md

Module-specific guidance for `erun-ui/playwright`. Follow the repository root `AGENTS.md` and `erun-ui/AGENTS.md` first, then apply this file for work in this subtree.

## Module Role

- `erun-ui/playwright` is a separate Yarn project that runs end-to-end UI tests for the desktop frontend.
- Tests drive `erun-app --headless` over the HTTP+SSE bridge instead of opening a Wails window. The same React bundle the desktop renders is served over loopback (each backend announces the address it bound; the default requested port is `34123`); method calls go through `/__erun_invoke`, events stream from `/__erun_events`, and `window.runtime` / `window.go.main.App` are shimmed at the document root.
- **The port a backend was asked for is never assumed to be the port it serves on.** `erun-app --headless --port N` treats `N` as a preference: a port another process already holds falls back to an OS-assigned one, and the backend announces the address it actually bound (`erun-ui/main.go` `listenHeadless`) on startup. Every caller reads that announcement — `fixtures/workerBackend.ts` resolves its `baseURL` from it, never from the port it requested. Do not reintroduce a probe-then-bind port check anywhere in the harness: a port free at probe time can be taken before the backend binds it, which is a race no probe can close and the only failure mode the announcement contract exists to absorb.
- The backend runs against an isolated, suite-owned config root with a deterministic seeded baseline — never against the developer's real `~/.erun` / `~/.config/erun`. See "Isolated config root and seeded baseline" below.
- Use this suite for cross-component flows that depend on rendered DOM and round-trip backend calls — sidebar toggles, dialog interactions, layout panels, status banners, activity drawer state. It does not replace `go test ./...`: Go tests cover backend logic, Playwright covers the React frontend behaviour after a real boot sequence.

## Isolated config root and seeded baseline

The suite owns its config root. `fixtures/seedRoot.ts` owns layout, names, and seeding:

- `run.sh` creates a throwaway root (`mktemp -d …/erun-playwright-home.XXXXXX`), exports it as `ERUN_PLAYWRIGHT_HOME`, and removes it again via an EXIT trap. When `playwright test` is invoked directly, `playwright.config.ts` creates the root itself at config-load time.
- Default: one backend and isolated root per worker (`fixtures/workerBackend.ts`), beneath `ERUN_PLAYWRIGHT_HOME`, preferring base-port + parallel index. The worker serves the address its own backend announced, so a preferred port another process holds costs it a different port, never the run. Seed at worker setup; global teardown removes the parent as a cleanup backstop. Do not add a shared `webServer` in this mode.
- k3d mode is the explicit exception: one worker, real cluster, shared backend and flat root. Worker fixtures reuse that backend instead of spawning another.
- Baseline: tenant `pw`, inert local-agent envs `alpha`/`beta`, alias `pw-aws`, and `aitool: sh`. Keep config fields aligned with integration fixtures; never launch real AI tools from inert tests.
- **Artifacts have one root, and a run can be told to keep them out of its tree.** `fixtures/artifacts.ts` owns it: Playwright's `outputDir`, the HTML report, and every frame a spec captures resolve through `artifactPath()`, so `ERUN_PLAYWRIGHT_ARTIFACTS_DIR` moves all three together. Unset it and they stay in the suite directory, which is what lets a reviewing orchestrator read a pod's frames out of the synced worktree — so the default stays. Both in-container gate paths set it to a container-local directory (the `erun-devops` Dockerfile's test stage, and `scripts/repro-gate-contention.sh`, which bind-mounts the worktree over `/src` and runs as root): artifacts a root container writes into an environment's own tree cannot be removed there, and every later run in that environment fails with a bare `EACCES` inside whichever spec writes first — for every branch, not just the one that poisoned it. New capture sites go through `artifactPath()`, never a bare repository-relative path.
- An artifact directory a run must not use — one it cannot write, or one owned by another user, so its writes would not be the tree owner's to remove — is refused at config load, naming the directory and the way out, instead of failing inside a spec.
- **`ERUN_PLAYWRIGHT_ARTIFACTS_DIR` does not cover the build outputs.** A root container that runs the frontend build over a bind-mounted worktree leaves `erun-ui/frontend/dist`, `erun-kit/dist` and `erun-console/dist` root-owned, and a later non-root build dies at `vite:prepare-out-dir` with a bare `EACCES` before any spec runs. That tree is not repair-able as the tree owner — the directory is writable but its contents are not, so the entries inside cannot be emptied. Clear it from a root container over the same mount (`docker run --rm -u 0 -v "$PWD":/w ghcr.io/sophium/erun-devops:<version> rm -rf <module>/dist`) rather than fighting the permissions by hand.
- `backendEnv()` in `fixtures/seedRoot.ts` also sets two determinism seams on the backend process, both consumed in `erun-ui/app.go`/`erun-ui/session.go` and set nowhere in production:
  - `ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE=0` prevents real host listeners from masquerading as seeded environments.
  - `ERUN_LOCAL_SHELL_OVERRIDE=1` selects an rc-free shell. Wait for exported `LOCAL_SHELL_PROMPT` before screen-position assertions; keep it aligned with the backend prompt.
- Specs import the seeded names (`SEED_TENANT`, `SEED_ENV_ALPHA`, `SEED_ENV_BETA`) from `fixtures/seedRoot.ts` and assert against them directly. Do not query the sidebar to "pick the first available row" — the baseline is deterministic, so a missing seeded row is a bug the spec should surface.
- Mutating specs use the unique `seededEnv` fixture, wait for fsnotify visibility, and remove their env on teardown. Session/tab/lease churn must not touch baseline rows.
- **Long-lived stubs are registered and reaped.** The `erun` (tab session) and `claude` (orchestrator session) stubs park for the life of the session the desktop spawned for them, because a row reads "running" only while its process is up. The desktop spawns them, so nothing in the harness would otherwise collect one: `writeStubBinary` (POSIX) and `fixtures/winstub` both call `register_stub`/`registerStub` first, appending the pid they park under and the stub's own name to `ERUN_PLAYWRIGHT_STUB_REGISTRY` (a file in the isolated root, set by `backendEnv()`), and `fixtures/stubProcesses.ts` reaps that registry on every spec's teardown (`stubReaper`, an automatic fixture in `fixtures/erunApp.ts`) with the worker teardown as the backstop. A registered pid is signalled only while its command line still identifies it as a stub, so a recycled pid is never killed. Keep POSIX and win32 in lockstep, and never park a long-lived stub without registering it — an unregistered stub is unreapable and accumulates for the run. `tests/areas/orchestrator/stub-process-lifetime.spec.ts` asserts that on the process table: a session a spec opens is backed by a live stub, and that stub is gone by the next spec.
- Specs that need state the baseline does not carry should stage exactly what they need (extend the seed, write a per-test env, or stub the RPC over `/__erun_invoke`) instead of skipping. Reserve `test.skip` for state that genuinely requires a live cluster or cloud host (a stopped EC2 context, a real runtime pod, a real Codex session); name the constraint in a comment.

## Headless Launch

There is only one supported way to run the suite. The shell script `run.sh` in this directory is the single entry point — `yarn` scripts call it for convenience, and the desktop build/packaging flow invokes it too.

- `run.sh` is wired through `scripts/agent-gate.sh`, the same wrapper `make check` uses: outside an agent pod it behaves exactly as documented below, but inside one it detaches the whole run — build, lint, and the suite itself — through erun's own job primitive and awaits it for a bounded window, so this suite (longer than `make check`) never sits as an ordinary foreground command for an agent's harness to auto-background. A timeout says to run the same `run.sh` invocation again to keep waiting.
- One-shot from this directory:
  ```sh
  ./run.sh
  ```
  Defaults: headless browser, preferred port `34123` (each worker prefers base + its index and serves the address its backend announced). Uses the existing `../bin/erun-app` if present; builds it only when missing. Packaging pipelines that produced the binary in an earlier step skip the build cost.
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
- `--port N` override the preferred backend port. Defaults to `34123` to avoid clashing with `wails dev`'s `34115`. Exported as `ERUN_PLAYWRIGHT_PORT` so `playwright.config.ts` stays in sync. It is a preference only: each worker's real port is the one its backend announced.
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

The default suite is inert and offline: it PATH-prepends stub `kubectl`/`helm`/`docker` plus an inert `erun` and `claude`, and pins `ERUN_APP_CLI` to the `erun` stub, so no real deploy ever runs. The `claude` stub is load-bearing beyond inertness: `orchestratorLaunchCommand` refuses to start a session whose tool is not on PATH, so an image without a resolvable `claude` leaves every orchestrator spec that needs a live session red — the stub makes those specs independent of the host image. That is by design (no hosted CI, #521) but it structurally cannot see the desktop's full create → build → push → deploy → open → MCP flow — the bug class #644 fixed. The **opt-in k3d mode** exercises that flow against a real local cluster.

- **How to run:** `./run.sh --e2e-k3d` (sets `ERUN_E2E_K3D=1`). It builds the real `erun` CLI for the desktop tabs, registers binfmt for the mandatory multi-arch build, brings a throwaway k3d cluster + built-in registry up in `global-setup`, runs **only** `tests/e2e/`, and tears the cluster down in `global-teardown` (the `run.sh` EXIT trap is the backstop).
- **Host preconditions:** running Docker, k3d, and binfmt for the default multi-architecture build. This live mode is separate from the default suite and integration gate.
- **Gating:** the e2e specs live under `tests/e2e/` and are excluded from the default run via `playwright.config.ts` `testIgnore` (not per-spec `test.skip`), so the default suite never collects them. `ERUN_E2E_K3D=1` flips the un-stubbed `backendEnv()` branch (real tools + real `erun`, only `aws` stubbed via `ERUN_AWS_BIN`) and includes the dir. Keep both directions intact: the k3d branch must never leak into the default inert mode, and the inert specs (which assume stubs) must never run against the real-tool backend.
- **Determinism still binding (#643):** cluster specs are the classic flake source. Wait on observable conditions (activity-queue trace lines, the rendered ERun tab, pod-Ready), never wall-clock; size per-spec `test.setTimeout` for the real build → push → deploy round-trip (minutes), which is far slower than any default spec.

## Area-scoped gate selection

The merge gate selects Playwright areas from the test diff; this is selection, not permission to omit regression specs:

- **Specs are organized by area, not flat.** `tests/smoke/` holds one spec file covering every area's shallowest critical path — it always runs, on every build, regardless of selection. `tests/areas/<area>/` holds each area's full spec set. `tests/harness/` holds the suite's own contracts, which belong to no area and drive no desktop flow (they take the base `@playwright/test` rather than `fixtures/erunApp.ts`); that placement is what makes the resolver classify a change to them as shared infrastructure. Area membership is directory position; nothing else declares it.
- **Selection follows changed specs, not a source-to-area glob map — with one deliberate exception.** A source-to-area glob map was considered and rejected (see the issue's history): it needs a glob for every new source directory and a missing one silently mis-selects. Keying on which spec files changed removes that class of bug — the signal is in the diff itself. That reasoning held for classifying _which area_ a change belongs to, but it was applied too broadly at first: the desktop application source the specs actually exercise (`erun-ui`'s own Go sources and `erun-ui/frontend`) was originally in neither list at all, so it fell through to the `smoke`-only default — a change that rewrote desktop behavior with no spec-file edit got smoke-only gate coverage while this file's own claim said the suite ran. The fix folds `erun-ui`'s application source (everything under `erun-ui/` except the Playwright suite itself) into the same "affects every area" bucket as the shared-infra paths below, rather than trying to build the rejected glob map after all — a change to `erun-ui`'s source is common enough, and cheap enough to over-cover, that the full suite is the right default whenever it's touched.
  - A change that adds or edits spec files in `tests/areas/<area>/` → `erun build` runs **smoke + that area** (smoke plus every area whose specs changed, for a multi-area diff).
  - A change that touches no spec file, and no `erun-ui` application source, at all → `erun build` runs **smoke only**.
  - A change under `tests/fixtures/`, `tests/pages/`, `global-setup.ts`, `global-teardown.ts`, `playwright.config.ts`, **or anywhere under `erun-ui/` outside the playwright suite itself** (Go sources, `erun-ui/frontend/**`) affects every area at once → `erun build` runs **everything** (smoke + every area).
  - The full suite always remains runnable on demand — `./run.sh` with no `PLAYWRIGHT_TEST_AREAS` set, `erun e2e`'s post-deploy backstop (#2092), a nightly run, or a developer iterating locally. This changes what the _gate_ selects, not what exists.
- **Fail-safe direction: an unresolvable selection runs everything, never zero.** `ResolvePlaywrightTestAreaSelection` (`erun-common/build_playwright_areas.go`) returns "could not resolve" when there is no git repository or no merge base against any candidate upstream branch, and the caller leaves `PLAYWRIGHT_TEST_AREAS` unset in that case — the Dockerfile's own `ARG PLAYWRIGHT_TEST_AREAS=""` default, which `run.sh` reads as "run everything". A gap in the mapping costs time, never coverage.
- **Mechanism:** resolve selection where git metadata exists, then pass `PLAYWRIGHT_TEST_AREAS` through the Docker build argument and `make check` environment into `run.sh`. Empty/unresolved means the full suite, never zero.
- **A plain local `make check`/`make test-playwright` resolves the identical selection now too, instead of always running the full suite.** Before this, a local run never went through `erun build`'s resolution above, so `PLAYWRIGHT_TEST_AREAS` stayed unset and the local run always cost the full ~21-23 minutes while the gate that actually protects `main` could run in tens of seconds — backwards, since a local run exists to preview the gate cheaply, not to be stricter than it. `erun exec resolve-playwright-areas` is a thin CLI wrapper around the same `ResolvePlaywrightTestAreaSelection` function `erun build` calls (always exits 0, printing `all` when resolution fails, matching the fail-safe direction above); the root Makefile's `test-playwright` target resolves it via a target-specific `PLAYWRIGHT_TEST_AREAS ?= $(shell cd erun-cli && go run . exec resolve-playwright-areas)`, which Make only evaluates when the caller hasn't already supplied the variable — so the Docker build-arg thread (including its empty-string default) is left untouched, and only a bare local invocation pays the resolution cost.
- **`erun e2e` (#2092) is the backstop**, not a replacement for this gate: it runs the full suite post-deploy against the real deployment, catching a cross-area regression this change-scoped selection missed (a shared-state change that breaks an area the diff didn't touch). Landing that backstop is what makes it safe to stop running everything pre-merge.

### Area taxonomy

`tests/areas/<area>/` is the canonical membership list, not a copied file-count
table. Reuse the appropriate existing area; when adding/reassigning areas, update
smoke coverage and selection tests in the same PR. Smoke must cover each area's
critical path.

- For changes to the desktop create → deploy → open flow or its deployed artifacts, also run the opt-in k3d e2e mode (`./run.sh --e2e-k3d`) on a Docker + k3d + binfmt host — it is the only signal that exercises the real runtime end-to-end.
- After failures, run `yarn report` to open the HTML report. Traces and screenshots for failed tests live under `playwright-report/` and `test-results/` — or under `ERUN_PLAYWRIGHT_ARTIFACTS_DIR` when the run set it, which is always the case in-container (see "Artifacts have one root" above).
