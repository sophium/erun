import { defineConfig, devices } from '@playwright/test';

// Single shared erun-app --headless instance backs every spec. The headless
// backend is a singleton (one process, one set of session state), so tests
// must serialise — fullyParallel is off and workers is pinned to 1. CI reuses
// the same constraints; locally we reuse an already-running server so
// `yarn test` is fast on re-runs.
const HEADLESS_PORT = 34123;

export default defineConfig({
  testDir: './tests',
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
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
});
