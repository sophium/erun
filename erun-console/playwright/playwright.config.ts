import { defineConfig } from '@playwright/test';

// The console's own real-browser suite: the landing-layout spec, measured
// against the built bundle. `vite preview` serves erun-console/dist (built
// beforehand by `yarn build` in erun-console; the console image's build stage
// does that), and nothing else is provisioned — platform discovery failing is
// the landing page's documented fallback. Suites against a deployed
// environment live in erun-devops/e2e/console and run through `erun e2e`.
const CONSOLE_PORT = 5179;
const consoleUrl = `http://127.0.0.1:${CONSOLE_PORT}/`;

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  // No retries — a pass that needs one is a determinism defect to fix, never
  // to mask.
  retries: 0,
  reporter: [['list']],
  timeout: 90_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: consoleUrl,
    browserName: 'chromium',
    trace: 'retain-on-failure',
  },
  // --strictPort plus reuseExistingServer: false refuse a port something else
  // already answers on, so the suite never measures whatever happens to be
  // listening there.
  webServer: {
    command: `yarn preview --host 127.0.0.1 --port ${CONSOLE_PORT} --strictPort`,
    cwd: '..',
    url: consoleUrl,
    reuseExistingServer: false,
    timeout: 60_000,
    stderr: 'pipe',
  },
});
