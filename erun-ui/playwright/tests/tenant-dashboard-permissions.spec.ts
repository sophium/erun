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
      // PanelBody's error branch used to render through a bespoke
      // DashboardMessage <div> with no ARIA role at all, so a screen reader
      // never announced it. It now renders through InlineAlert (role="alert"),
      // queried here via the accessibility tree rather than an attribute match.
      await expect(app.tenantDashboard.activePanel().getByRole('alert')).toContainText('http 500');
      await page.screenshot({ path: 'test-results/dashboard-panel-error-alert-light.png' });
      await app.titlebar.toggleTheme();
      await page.screenshot({ path: 'test-results/dashboard-panel-error-alert-dark.png' });
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

// #1390: a stale signed-in identity fails the dashboard's own precondition
// read (whoami) before any panel is even attempted, so it blocks the whole
// dashboard — apiError, not a per-panel error. That message names "sign in"
// as its fix, and the app already has a control for it (the same cloud-alias
// login the sidebar offers), so it must render with that control rather than
// as inert text in an empty panel.
test.describe('tenant dashboard — a stale identity blocks the whole dashboard (#1390)', () => {
  // #1392: a successful sign-in used to leave this exact alert and button on
  // screen — the login dispatched and the sidebar's alias updated, but
  // nothing re-fetched the dashboard, so the operator saw the identical
  // stale-identity error next to a session that had just been fixed. The
  // route stub answers LoadTenantDashboard differently on the re-fetch
  // triggered by a successful login than it did on the initial load, so this
  // proves the panel actually recovers rather than merely dispatching a
  // login that changes nothing on screen.
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

      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });
      await app.sidebar.openTenantDashboard(SEED_TENANT);

      await expect(app.tenantDashboard.notSignedInHeading()).toBeVisible();

      const signIn = app.tenantDashboard.signInButton();
      await expect(signIn).toBeVisible();
      await signIn.click();
      await expect.poll(() => loginAlias).toBe('pw-aws');

      // The panel must recover: the not-signed-in state and its Log in
      // button are gone, replaced by the freshly re-fetched dashboard.
      await expect(app.tenantDashboard.notSignedInHeading()).toHaveCount(0);
      await expect(page.getByRole('button', { name: 'Log in' })).toHaveCount(0);
      await app.tenantDashboard.waitForOpen();
      await expect.poll(() => dashboardLoads).toBe(2);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The mirror case: a sign-in that does not succeed must say so rather than
  // silently re-rendering the identical message, which is the same trap one
  // level down (#1392).
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

      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });
      await app.sidebar.openTenantDashboard(SEED_TENANT);

      await expect(app.tenantDashboard.notSignedInHeading()).toBeVisible();
      const signIn = app.tenantDashboard.signInButton();
      await signIn.click();

      // The real reason, not a generic "Sign-in failed" sentence. Scoped to
      // main: a toast notification carries the same message too, and both
      // are role="alert".
      await expect(
        page.getByRole('main').getByRole('alert').filter({ hasText: 'sign-in was refused' }),
      ).toBeVisible();
      // The not-signed-in state is still there — the operator can try again —
      // but no extra dashboard re-fetch happened.
      await expect(app.tenantDashboard.notSignedInHeading()).toBeVisible();
      await expect.poll(() => dashboardLoads).toBe(1);
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

      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });
      await app.sidebar.openTenantDashboard(SEED_TENANT);

      await expect(app.tenantDashboard.noPermissionHeading()).toBeVisible();
      await expect(page.getByRole('button', { name: 'Log in' })).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
