import type { Request, Route } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// A local tenant reaches a platform tenant only through its alias credential,
// so the header must name whose rows are shown.

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

// readyDashboard stages whoami's tenantName; '' means the platform names none,
// and a name other than SEED_TENANT is a mismatch with no rows.
function readyDashboard(environment: string, tenantName: string): Record<string, unknown> {
  const mismatched = Boolean(tenantName) && tenantName !== SEED_TENANT;
  return {
    tenant: SEED_TENANT,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    ...(mismatched ? { platformState: 'tenant-mismatch' } : {}),
    user: {
      tenantId: 'tenant-1',
      ...(tenantName ? { tenantName } : {}),
      userId: 'user-1',
      username: 'reader',
    },
    panels: [{ tab: 'users' }],
  };
}

test.describe('tenant dashboard — the header names the platform tenant', () => {
  test('names the platform tenant when it is not the local tenant the dashboard was opened from', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('platform-tenant-mismatch');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(page, readyDashboard(environment, 'erun'));

      await app.sidebar.openTenantDashboard(SEED_TENANT);

      // The mismatch body replaces the tab strip, so assert the header directly.
      await expect(app.tenantDashboard.platformTenantLine()).toContainText('erun');
      await expect(app.tenantDashboard.tabs()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('names the platform tenant even when it matches the local tenant name', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('platform-tenant-match');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      // The agreeing case still shows the name, so absence means only "unreported".
      await stubLoadTenantDashboard(page, readyDashboard(environment, SEED_TENANT));

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      await expect(app.tenantDashboard.platformTenantLine()).toContainText(SEED_TENANT);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('omits the line for a platform that reports no tenant name', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('platform-tenant-unnamed');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      // An older platform omits the name; the dashboard must not echo the local one.
      await stubLoadTenantDashboard(page, readyDashboard(environment, ''));

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      await expect(app.tenantDashboard.platformTenantLine()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
