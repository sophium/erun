import { defineConfig } from '@playwright/test';

// The deployed console, reached at the URL `erun e2e` resolved from the
// environment's exposed `console` service. TLS is verified: the exposed
// certificate is real, and checking it is part of what this suite proves.
const baseURL = process.env.ERUN_E2E_BASE_URL;
if (baseURL === undefined || baseURL.trim() === '') {
  throw new Error(
    'ERUN_E2E_BASE_URL is required: run this suite through `erun e2e`, or pass --build-arg ERUN_E2E_BASE_URL=https://<console host>',
  );
}

export default defineConfig({
  testDir: './tests',
  // One deployment and one test identity: the suites sign in serially.
  fullyParallel: false,
  workers: 1,
  forbidOnly: true,
  // No retries: a pass that needs one is a determinism defect, not a pass.
  retries: 0,
  reporter: [['list']],
  // A cold sign-in includes discovery, the IdP's login round-trips and the
  // PKCE token exchange, over the public network.
  timeout: 90_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL,
    browserName: 'chromium',
    trace: 'retain-on-failure',
  },
});
