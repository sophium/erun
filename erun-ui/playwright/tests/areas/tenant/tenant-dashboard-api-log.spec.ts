import type { Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The dashboard loads only once an env has an apiUrl, so stage one; the RPC
// itself is stubbed.
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

// The panel must render the raw transport error unchanged.
const RAW_ERROR = 'exit status 1: kubectl stub: no cluster in the Playwright harness';

// A failed read must name the read it could not make, with the raw error kept
// beneath it.
test.describe('tenant dashboard — API log tab', () => {
  test('a failed read names the read that failed and keeps the raw error', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('api-log-error');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

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
      // A failed read must never read as "nothing logged yet".
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
