# AGENTS.md

Module-specific guidance for `erun-ui/playwright`. Follow the repository root `AGENTS.md` and `erun-ui/AGENTS.md` first, then apply this file for work in this subtree.

## Module Role

- `erun-ui/playwright` is a separate Yarn project that runs end-to-end UI tests for the desktop frontend.
- Tests drive `erun-app --headless` over the HTTP+SSE bridge instead of opening a Wails window. The same React bundle the desktop renders is served at `http://127.0.0.1:34123/`; method calls go through `/__erun_invoke`, events stream from `/__erun_events`, and `window.runtime` / `window.go.main.App` are shimmed at the document root.
- Use this suite for cross-component flows that depend on rendered DOM and round-trip backend calls — sidebar toggles, dialog interactions, layout panels, status banners, activity drawer state. It does not replace `go test ./...`: Go tests cover backend logic, Playwright covers the React frontend behaviour after a real boot sequence.

## Headless Launch

There is only one supported way to run the suite. The shell script `run.sh` in this directory is the single entry point — `yarn` scripts call it for convenience, and the desktop build/packaging flow invokes it too.

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
- `--port N` override the backend port. Defaults to `34123` to avoid clashing with `wails dev`'s `34115`. Exported as `ERUN_PLAYWRIGHT_PORT` so `playwright.config.ts` stays in sync.
- `--headed` run the browser with a visible window. Otherwise headless.
- `--` everything after this is forwarded to `playwright test` (e.g. `./run.sh -- --grep sidebar`).
- Any unrecognised flag is also forwarded to `playwright test`, so `yarn test --grep sidebar` works even though Yarn 1 strips its own `--` separator before reaching the script.

`run.sh` is the canonical entry point from desktop build/packaging flows — `build.sh`-style automation should call `erun-ui/playwright/run.sh` rather than chaining the underlying `yarn` and `playwright` commands by hand. Packaging pipelines that produce `bin/erun-app` themselves can call `./run.sh` directly; the script will reuse the binary.

## Frontend And Backend Split

- Page object classes go in `pages/`. Each file owns one component surface (sidebar, titlebar, a single dialog, a single panel) and exposes high-level actions rather than raw locators or selectors.
- Tests in `tests/` consume POMs through the fixture in `fixtures/erunApp.ts` (`test`, `expect` re-exports). Avoid calling `page.click(...)` or `page.locator(...)` directly from specs.
- Keep specs deterministic. Do not assume environment names or tenant counts; query the sidebar at the start of the test and pick the first available row. The headless backend reflects the developer's actual `~/.erun/` config, so hard-coded names will diverge across machines.

## Selector Conventions

- Prefer accessible queries: `getByRole`, `getByLabel`, `getByText`. Match the `aria-label` / `aria-labelledby` values the production React components already set.
- When two surfaces share a role (`tablist`, `dialog`), scope the locator to the parent component's POM — e.g. `ManageDialog.getActiveTab()` queries inside the manage dialog locator, not at the document level.
- If a target has no accessible label, fix the component first (add `aria-label`), then write the test. Reaching for `data-testid` is acceptable only when no semantic equivalent exists.

## Working Rules

- Boot races are real. The fixture's `AppShell.open()` waits for the "Loading environments..." overlay to clear before yielding control. New specs that mount their own page state should rely on the fixture rather than calling `page.goto` directly.
- The Cancel buttons on `Init` and `Manage` dialogs sit below the default 900 px viewport. The config uses `1440x1200`; keep tall-dialog tests on that viewport rather than shrinking it.
- The headless backend is a singleton; `playwright.config.ts` sets `fullyParallel: false` and `workers: 1`. Keep it that way unless you split the backend into per-test processes.
- Treat assertion failures as bugs to investigate, not noise to skip. If a test reveals a real frontend regression, fix the frontend; if a flow is genuinely flaky and the cause is environmental (clock skew, port reuse), fix the harness. Use `test.fixme(...)` only with a justification in a comment and a follow-up issue.

## Validation

- Run `./run.sh` (or `yarn test`) before pushing changes that touch any frontend component, slice, thunk, or controller method exposed to the React tree.
- After failures, run `yarn report` to open the HTML report. Traces and screenshots for failed tests live under `playwright-report/` and `test-results/`.
