import type { Request, Route } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// tenant-dashboard-platform-tenant.spec.ts covers the header naming whose rows
// the dashboard is rendering. A local tenant is bound to a platform tenant only
// by whichever cloud alias its credential reaches, so a dashboard opened from
// one local tenant can render a different platform tenant's reviews, queue,
// users and audit. See tenant-dashboard-refresh.spec.ts for why this stages a
// throwaway env with an apiUrl and stubs the RPC rather than using the seeded
// baseline or a real backend.

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

// readyDashboard is a loaded dashboard for SEED_TENANT, with the caller's own
// identity row as whoami reports it. tenantName is what the platform calls the
// tenant the bearer resolves to; '' stages a platform that names none.
function readyDashboard(environment: string, tenantName: string): Record<string, unknown> {
  return {
    tenant: SEED_TENANT,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
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

      // The reported state: the local tenant is 'pw' (SEED_TENANT) and the
      // credential reaches a platform tenant called something else. Before
      // this, the heading named 'pw' and every panel below rendered another
      // tenant's rows with nothing on the surface saying so.
      await stubLoadTenantDashboard(page, readyDashboard(environment, 'erun'));

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      await expect(app.tenantDashboard.platformTenantLine()).toContainText('erun');
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

      // The agreeing case still names it, so the line's absence means only
      // "the platform reported no name" -- never "the two agreed", which is
      // the reading that let the mismatch go unnoticed.
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

      // An older platform answers whoami without a tenant name. The dashboard
      // still loads; it simply has no name to show, and must not invent one by
      // echoing the local tenant.
      await stubLoadTenantDashboard(page, readyDashboard(environment, ''));

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      await expect(app.tenantDashboard.platformTenantLine()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
