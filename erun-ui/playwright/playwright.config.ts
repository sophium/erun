import { defineConfig, devices } from '@playwright/test';
import { backendEnv, e2eK3dEnabled, isolatedRoot } from './fixtures/seedRoot.js';

// The headless backend is a singleton (one process, one session-state set),
// so specs must serialise rather than run in parallel.
//
// run.sh and this config share ERUN_PLAYWRIGHT_PORT so an overridden port
// reaches both; the default stays clear of wails dev's 34115.
const HEADLESS_PORT = Number(process.env.ERUN_PLAYWRIGHT_PORT) || 34123;

// Resolve (and, without run.sh, create) the isolated config root at
// config-load time, before workers fork, so every process in the run agrees
// on one ERUN_PLAYWRIGHT_HOME. See fixtures/seedRoot.ts.
isolatedRoot();

export default defineConfig({
  testDir: './tests',
  // The k3d e2e specs need a real cluster and the un-stubbed backend, so they
  // must never run in the default inert mode. Excluding the dir here (not
  // per-spec test.skip) keeps the default suite from ever collecting them.
  testIgnore: e2eK3dEnabled() ? [] : ['**/tests/e2e/**'],
  globalSetup: './global-setup',
  globalTeardown: './global-teardown',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  // No retries: a spec that only passes on a retry is flaky, and flakiness is a
  // determinism defect to fix, never to mask (see AGENTS.md "No flaky tests").
  retries: 0,
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
    // Retries are off, so capture the trace on every failure (not just on a
    // retry that never happens) to keep failures debuggable via `yarn report`.
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        // devices['Desktop Chrome'] resets the viewport, so re-apply the tall
        // one (see top-level use.viewport) to keep dialog footer buttons reachable.
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
    env: backendEnv(),
  },
});
