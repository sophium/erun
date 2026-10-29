import { defineConfig } from '@playwright/test';

// TLS stays verified: the exposed certificate is part of what this suite proves.
const baseURL = process.env.ERUN_E2E_BASE_URL;
if (baseURL === undefined || baseURL.trim() === '') {
  throw new Error(
    'ERUN_E2E_BASE_URL is required: run this suite through `erun e2e`, or pass --build-arg ERUN_E2E_BASE_URL=https://<console host>',
  );
}

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  forbidOnly: true,
  // A pass that needs a retry is a determinism defect.
  retries: 0,
  reporter: [['list']],
  timeout: 90_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL,
    browserName: 'chromium',
    trace: 'retain-on-failure',
  },
});
