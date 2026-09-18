import type { Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The tenant dashboard only calls LoadTenantDashboard once it can resolve both
// an API URL (from an environment) and a primary cloud alias (from the tenant
// config, which the seeded `pw` tenant already normalizes to pw-aws). None of
// the baseline envs carry an apiUrl, so this stages one throwaway env that does,
// purely to satisfy that gate; the RPC itself is stubbed below rather than
// hitting a real backend, per playwright/AGENTS.md's "stub the RPC over
// /__erun_invoke" guidance for state the seeded baseline does not carry.
function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

async function stubLoadTenantDashboard(
  page: import('@playwright/test').Page,
  data: Record<string, unknown>,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadTenantDashboard') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
      return;
    }
    await route.continue();
  });
}

function dashboardData(
  environment: string,
  extra: Record<string, unknown>,
): Record<string, unknown> {
  return {
    tenant: SEED_TENANT,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    user: { tenantId: 't1', userId: 'u1', username: 'operator' },
    ...extra,
  };
}

// The raw transport error, as the backend hands it over: the panel must render
// it unchanged, not paraphrase it away.
const RAW_ERROR = 'exit status 1: kubectl stub: no cluster in the Playwright harness';

// The API log is one tab of nine read over the environment's own MCP edge, so
// nothing else on screen connects a transport error to this panel. A failed
// read has to name the read it could not make, with the raw error kept visible
// beneath that subject for whoever needs the original text.
test.describe('tenant dashboard — API log tab', () => {
  test('a failed read names the read that failed and keeps the raw error', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('api-log-error');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      // What LoadTenantDashboard returns for a failed API log read.
      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, {
          apiLogError: `Could not read this environment's API log. ${RAW_ERROR}`,
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('API log');

      const alert = app.tenantDashboard.apiLogAlert();
      await expect(alert).toBeVisible();
      await expect(alert).toContainText("Could not read this environment's API log");
      await expect(alert).toContainText(RAW_ERROR);
      // A failed read must never be readable as "nothing logged yet".
      await expect(app.tenantDashboard.apiLogEmptyState()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a log with nothing in it stays distinct from a failed read', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('api-log-empty');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(page, dashboardData(environment, { apiLog: '' }));

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('API log');

      await expect(app.tenantDashboard.apiLogEmptyState()).toBeVisible();
      await expect(app.tenantDashboard.apiLogAlert()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a returned log renders its container output', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('api-log-populated');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, { apiLog: 'level=info msg="served /v1/whoami"' }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('API log');

      await expect(app.tenantDashboard.apiLogBody()).toContainText('level=info');
      await expect(app.tenantDashboard.apiLogAlert()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
