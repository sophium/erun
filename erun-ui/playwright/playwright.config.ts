import { defineConfig, devices } from '@playwright/test';
import { backendEnv, isolatedRoot } from './fixtures/seedRoot.js';

// Single shared erun-app --headless instance backs every spec. The headless
// backend is a singleton (one process, one set of session state), so tests
// must serialise — fullyParallel is off and workers is pinned to 1.
//
// run.sh exports ERUN_PLAYWRIGHT_PORT so the wrapper script and this config
// stay in sync when callers override the port. Falls back to 34123 (clear
// of wails dev's 34115) when invoked directly.
const HEADLESS_PORT = Number(process.env.ERUN_PLAYWRIGHT_PORT) || 34123;

// Resolve (and, when launched without run.sh, create) the suite-owned
// isolated config root at config-load time, before workers fork, so every
// process in the run agrees on ERUN_PLAYWRIGHT_HOME. global-setup seeds the
// deterministic baseline under it; the webServer env below points the
// backend at it. See fixtures/seedRoot.ts.
isolatedRoot();

export default defineConfig({
  testDir: './tests',
  globalSetup: './global-setup',
  globalTeardown: './global-teardown',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  timeout: 30_000,
  expect: {
    timeout: 10_000,
  },
  use: {
    baseURL: `http://127.0.0.1:${HEADLESS_PORT}`,
    headless: true,
    // The env-init and manage dialogs render all sections inline and can
    // be taller than a normal 900px viewport. Bump the height so footer
    // buttons stay reachable without artificial scrolling.
    viewport: { width: 1440, height: 1200 },
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        // Override devices['Desktop Chrome']'s viewport so the env-init
        // and manage dialogs' footer buttons stay reachable without
        // scrolling tricks. See top-level `use.viewport` for the rationale.
        viewport: { width: 1440, height: 1200 },
      },
    },
  ],
  webServer: {
    command: `./bin/erun-app --headless --port ${HEADLESS_PORT}`,
    url: `http://127.0.0.1:${HEADLESS_PORT}/`,
    cwd: '..',
    // The backend must boot against this run's isolated root. A leftover
    // dev server on the same port would be pointed at a stale (or worse,
    // the developer's real) config root, so never reuse one.
    reuseExistingServer: false,
    timeout: 30_000,
    stdout: 'pipe',
    stderr: 'pipe',
    // HOME + XDG_* redirect the backend's config and runtime-state roots
    // into the isolated root; the PATH prepend routes external binaries to
    // the inert stubs. Merged over process.env by Playwright.
    env: backendEnv(),
  },
});
