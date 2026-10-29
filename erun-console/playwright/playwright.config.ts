import { defineConfig } from '@playwright/test';

// Serves the prebuilt erun-console/dist with no API behind it; discovery failing is the
// landing page's documented fallback.
const CONSOLE_PORT = 5179;
const consoleUrl = `http://127.0.0.1:${CONSOLE_PORT}/`;

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  // A pass that needs a retry is a determinism defect.
  retries: 0,
  reporter: [['list']],
  timeout: 90_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: consoleUrl,
    browserName: 'chromium',
    trace: 'retain-on-failure',
  },
  // Never measure whatever else is already listening on the port.
  webServer: {
    command: `yarn preview --host 127.0.0.1 --port ${CONSOLE_PORT} --strictPort`,
    cwd: '..',
    url: consoleUrl,
    reuseExistingServer: false,
    timeout: 60_000,
    stderr: 'pipe',
  },
});
