import type { Page, Route, Request } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The roster comes from a hosted API the harness cannot reach, so the RPC is
// stubbed.
function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

async function stubLoadTenantDashboard(page: Page, data: Record<string, unknown>): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadTenantDashboard') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
      return;
    }
    await route.continue();
  });
}

// dashboardData carries both the caller (whoami) and the roster; an unreadable
// roster must not fall back to the caller's row.
function dashboardData(
  environment: string,
  panels: { tab: string; restricted?: string; error?: string }[],
  extra: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    tenant: SEED_TENANT,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    user: { tenantId: 't1', userId: 'u1', username: 'operator', roles: ['Auditor'] },
    panels,
    ...extra,
  };
}

test.describe('tenant dashboard — the Users tab lists the tenant (#2281)', () => {
  test('renders every user on the tenant, not only the signed-in one', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('users-roster');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, [{ tab: 'users' }, { tab: 'audit' }], {
          users: [
            { tenantId: 't1', userId: 'u1', username: 'operator', roles: ['Auditor'] },
            { tenantId: 't1', userId: 'u2', username: 'pat' },
            { tenantId: 't1', userId: 'u3', username: 'sam' },
          ],
        }),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Users');

      await expect(app.tenantDashboard.usersRows()).toHaveCount(3);
      await expect(app.tenantDashboard.usersTable()).toContainText('operator');
      await expect(app.tenantDashboard.usersTable()).toContainText('pat');
      await expect(app.tenantDashboard.usersTable()).toContainText('sam');
      await expect(app.tenantDashboard.usersEmptyState()).toHaveCount(0);
      // GET /v1/users reports no roles, so the row must not claim the user has none.
      await expect(app.tenantDashboard.usersRows().nth(1)).toContainText('Not reported');
      await expect(app.tenantDashboard.usersRows().nth(0)).toContainText('Auditor');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('names the unreadable roster instead of showing the caller alone', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('users-roster-restricted');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, [
          { tab: 'users', restricted: 'GET /v1/users' },
          { tab: 'audit' },
        ]),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      await expect(app.tenantDashboard.tab('Users')).toHaveCount(0);
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText('GET /v1/users');
      await expect(app.tenantDashboard.activePanel().getByRole('table')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('reports a failed roster read rather than falling back to one row', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('users-roster-failed');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, [
          { tab: 'users', error: 'load tenant dashboard GET /v1/users: http 500' },
          { tab: 'audit' },
        ]),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      // A failed panel keeps its tab, unlike a missing permission.
      await app.tenantDashboard.selectTab('Users');
      await expect(app.tenantDashboard.activePanel().getByRole('alert')).toContainText('http 500');
      await expect(app.tenantDashboard.activePanel().getByRole('table')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
