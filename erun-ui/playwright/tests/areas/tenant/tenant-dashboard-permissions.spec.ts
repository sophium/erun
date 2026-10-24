import type { Route, Request } from '@playwright/test';

import { artifactPath } from '../../../fixtures/artifacts.js';
import { expect, test, waitForSeededRow, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The capability set comes from a hosted API the harness cannot reach, so the
// RPC is stubbed.
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
      await waitForSeededRow(app, SEED_TENANT, environment);

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
      await waitForSeededRow(app, SEED_TENANT, environment);

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

      // A failed panel keeps its tab, unlike a missing permission.
      await app.tenantDashboard.selectTab('Builds');
      await expect(app.tenantDashboard.activePanel()).toContainText('http 500');
      await expect(app.tenantDashboard.activePanel().getByRole('alert')).toContainText('http 500');
      await page.screenshot({
        path: artifactPath('test-results/dashboard-panel-error-alert-light.png'),
      });
      await app.titlebar.toggleTheme();
      await page.screenshot({
        path: artifactPath('test-results/dashboard-panel-error-alert-dark.png'),
      });
      await app.titlebar.toggleTheme();

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
      await waitForSeededRow(app, SEED_TENANT, environment);

      await stubLoadTenantDashboard(
        page,
        dashboardData(
          environment,
          [
            { tab: 'users', restricted: 'GET /v1/whoami' },
            { tab: 'reviews', restricted: 'GET /v1/reviews' },
            { tab: 'queue', restricted: 'GET /v1/reviews/merge-queue' },
            { tab: 'pipeline', restricted: 'GET /v1/pipeline' },
            { tab: 'builds', restricted: 'GET /v1/reviews' },
            { tab: 'audit', restricted: 'GET /v1/audit-events' },
            { tab: 'registration', restricted: 'GET /v1/contexts' },
            { tab: 'requests', restricted: 'GET /v1/invite-requests' },
          ],
          // The summary offers the grant for each missing read a role covers.
          {
            accessRemedies: {
              'GET /v1/audit-events': {
                command: 'erun platform user grant-role --user-id u1 --role-id role-audit',
                roleName: 'Auditor',
              },
            },
          },
        ),
      );

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpenRestricted();

      // The API log is read over the env's MCP edge, so it needs no platform permission.
      await expect(app.tenantDashboard.tabs()).toHaveCount(1);
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText(
        'GET /v1/audit-events',
      );
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText(
        'Ask an administrator for access',
      );
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText(
        'erun platform user grant-role --user-id u1 --role-id role-audit',
      );
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText('Auditor');
      // No empty table that could read as "this tenant has nothing".
      await expect(app.tenantDashboard.activePanel().getByRole('table')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

// A stale identity fails whoami before any panel, so it blocks the whole
// dashboard and must offer the sign-in control.
test.describe('tenant dashboard — a stale identity blocks the whole dashboard (#1390)', () => {
  // A successful sign-in must re-fetch; the stub answers the re-fetch differently.
  test('offers to sign in, and a successful sign-in re-fetches the dashboard so the panel recovers', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('identity-stale');
    try {
      let loginAlias = '';
      let dashboardLoads = 0;
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = JSON.parse(request.postData() ?? '{}') as { method: string; args?: string[] };
        if (body.method === 'LoadTenantDashboard') {
          dashboardLoads += 1;
          if (dashboardLoads === 1) {
            await route.fulfill({
              contentType: 'application/json',
              body: JSON.stringify({
                data: {
                  tenant: SEED_TENANT,
                  platformState: 'not-signed-in',
                  platformAlias: 'pw-aws',
                },
              }),
            });
            return;
          }
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({
              data: {
                tenant: SEED_TENANT,
                environment,
                apiUrl: 'http://127.0.0.1:1/unreachable',
                platformState: '',
                user: { tenantId: 't1', userId: 'u1', username: 'operator' },
              },
            }),
          });
          return;
        }
        if (body.method === 'LoginCloudProvider') {
          loginAlias = body.args?.[0] ?? '';
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({ data: { alias: 'pw-aws', provider: 'aws', status: 'active' } }),
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);

      await expect(app.tenantDashboard.notSignedInHeading()).toBeVisible();

      const signIn = app.tenantDashboard.signInButton();
      await expect(signIn).toBeVisible();
      await signIn.click();
      // expect.poll defaults to expect.timeout, not the test deadline.
      await expect.poll(() => loginAlias, withTestBudget()).toBe('pw-aws');

      await expect(app.tenantDashboard.notSignedInHeading()).toHaveCount(0);
      await expect(page.getByRole('button', { name: 'Log in' })).toHaveCount(0);
      await app.tenantDashboard.waitForOpen();
      await expect.poll(() => dashboardLoads, withTestBudget()).toBe(2);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a failed sign-in reports the failure and leaves the error in place', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('identity-stale-login-fails');
    try {
      let dashboardLoads = 0;
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = JSON.parse(request.postData() ?? '{}') as { method: string };
        if (body.method === 'LoadTenantDashboard') {
          dashboardLoads += 1;
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({
              data: {
                tenant: SEED_TENANT,
                platformState: 'not-signed-in',
                platformAlias: 'pw-aws',
              },
            }),
          });
          return;
        }
        if (body.method === 'LoginCloudProvider') {
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({ error: 'sign-in was refused' }),
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);

      await expect(app.tenantDashboard.notSignedInHeading()).toBeVisible();
      const signIn = app.tenantDashboard.signInButton();
      await signIn.click();

      // Scoped to main: the toast carries the same message, also role="alert".
      await expect(
        page.getByRole('main').getByRole('alert').filter({ hasText: 'sign-in was refused' }),
      ).toBeVisible();
      // Still signed out, but with no extra re-fetch.
      await expect(app.tenantDashboard.notSignedInHeading()).toBeVisible();
      await expect.poll(() => dashboardLoads, withTestBudget()).toBe(1);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a permission refusal blocking the whole dashboard offers no button', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('identity-forbidden');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = JSON.parse(request.postData() ?? '{}') as { method: string };
        if (body.method === 'LoadTenantDashboard') {
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({
              data: {
                tenant: SEED_TENANT,
                platformState: 'no-permission',
                platformAlias: 'pw-aws',
              },
            }),
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);

      await expect(app.tenantDashboard.noPermissionHeading()).toBeVisible();
      await expect(page.getByRole('button', { name: 'Log in' })).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The sign-in answer is held just past expect's 10s default, so a poll not
  // using the test budget would fail here.
  test('a sign-in answer that lands past the poll cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    const environment = seedDashboardEnvironment('identity-stale-hold');
    try {
      let loginAlias = '';
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = JSON.parse(request.postData() ?? '{}') as { method: string; args?: string[] };
        if (body.method === 'LoadTenantDashboard') {
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({
              data: {
                tenant: SEED_TENANT,
                platformState: 'not-signed-in',
                platformAlias: 'pw-aws',
              },
            }),
          });
          return;
        }
        if (body.method === 'LoginCloudProvider') {
          await new Promise((resolve) => setTimeout(resolve, 12_000));
          loginAlias = body.args?.[0] ?? '';
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({ data: { alias: 'pw-aws', provider: 'aws', status: 'active' } }),
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await expect(app.tenantDashboard.notSignedInHeading()).toBeVisible();

      await app.tenantDashboard.signInButton().click();
      await expect.poll(() => loginAlias, withTestBudget()).toBe('pw-aws');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
