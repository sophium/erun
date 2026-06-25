import { defineConfig } from '@playwright/test';

// E2E for the console's OIDC Authorization Code + PKCE sign-in (issue #684),
// driven through a REAL Zitadel v4 instance (core + Login V2 container) — not a
// mock. run.sh brings up the whole stack (Zitadel, a migrated erun API, the
// console dev server), provisions the OIDC app, then runs this spec.
//
// E2E_CONSOLE_URL points the browser at the console dev server (run.sh sets it).
const consoleUrl = process.env.E2E_CONSOLE_URL ?? 'http://localhost:5173/';

export default defineConfig({
  testDir: './tests',
  // One real IdP + one shared backend: the sign-in flow is inherently serial.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  // No retries — a sign-in that only passes on a retry is a determinism defect
  // to fix, never to mask (root AGENTS.md "No flaky tests").
  retries: 0,
  reporter: [['list']],
  // Generous: a cold sign-in includes discovery, the Login V2 round-trips, the
  // PKCE token exchange, and the first-sign-in tenant bootstrap.
  timeout: 60_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: consoleUrl,
    browserName: 'chromium',
    trace: 'retain-on-failure',
  },
});
