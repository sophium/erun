import type { Route, Request } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// See tenant-dashboard-audit-log.spec.ts for why this stages a throwaway env
// with an apiUrl and stubs the RPC: the capability set these tests stage comes
// from a hosted erun-backend-api the inert harness deliberately has no access
// to, so the permission-derived rendering is exercised over the stubbed RPC.
// The Go side that derives these panels from the platform's capability answer is
// covered by erun-ui/tenant_dashboard_test.go.
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

test.describe('tenant dashboard — permission-derived surfaces (#1210)', () => {
  test('a tab the user may not open is absent, and the missing access is named', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('permissions-hidden-tabs');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      await stubLoadTenantDashboard(
        page,
        dashboardData(
          environment,
          [
            { tab: 'users' },
            { tab: 'queue', restricted: 'GET /v1/reviews/merge-queue' },
            { tab: 'builds', restricted: 'GET /v1/reviews' },
            { tab: 'audit' },
          ],
          {
            auditEvents: [
              {
                type: 'API',
                actor: 'subject-1',
                action: 'GET /v1/audit-events',
                createdAt: '2026-01-01T00:00:00Z',
              },
            ],
          },
        ),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      await expect(app.tenantDashboard.tab('Merge queue')).toHaveCount(0);
      await expect(app.tenantDashboard.tab('Builds')).toHaveCount(0);
      await expect(app.tenantDashboard.tab('Users')).toBeVisible();
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText(
        'GET /v1/reviews/merge-queue',
      );
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText('GET /v1/reviews');

      // The panel this caller may read still works while the others are hidden.
      await app.tenantDashboard.selectTab('Audit log');
      await expect(app.tenantDashboard.auditRows()).toHaveCount(1);
      await expect(app.tenantDashboard.auditTable()).toContainText('GET /v1/audit-events');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a panel that failed reports its own failure without blanking the others', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('permissions-partial-failure');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      await stubLoadTenantDashboard(
        page,
        dashboardData(
          environment,
          [
            { tab: 'users' },
            { tab: 'queue' },
            { tab: 'builds', error: 'load tenant dashboard GET /v1/reviews: http 500' },
            { tab: 'audit' },
          ],
          {
            auditEvents: [
              {
                type: 'CLI',
                actor: 'subject-2',
                action: 'erun build',
                createdAt: '2026-01-02T00:00:00Z',
              },
            ],
          },
        ),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      // A failed panel keeps its tab, so the failure is reachable rather than
      // hidden the way a missing permission is.
      await app.tenantDashboard.selectTab('Builds');
      await expect(app.tenantDashboard.activePanel()).toContainText('http 500');

      await app.tenantDashboard.selectTab('Audit log');
      await expect(app.tenantDashboard.auditRows()).toHaveCount(1);
      await expect(app.tenantDashboard.auditTable()).toContainText('erun build');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a caller who may read nothing is told why, instead of seeing empty panels', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('permissions-none');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      await stubLoadTenantDashboard(
        page,
        dashboardData(environment, [
          { tab: 'users', restricted: 'GET /v1/whoami' },
          { tab: 'reviews', restricted: 'GET /v1/reviews' },
          { tab: 'queue', restricted: 'GET /v1/reviews/merge-queue' },
          { tab: 'builds', restricted: 'GET /v1/reviews' },
          { tab: 'audit', restricted: 'GET /v1/audit-events' },
        ]),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpenRestricted();

      // Only the API log survives: it is read over the environment's own MCP
      // edge, not the platform API, so it carries no platform permission.
      await expect(app.tenantDashboard.tabs()).toHaveCount(1);
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText(
        'GET /v1/audit-events',
      );
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText(
        'Ask an administrator for access',
      );
      // Nothing renders as an empty table the user could read as "this tenant
      // has nothing in it".
      await expect(app.tenantDashboard.activePanel().getByRole('table')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
