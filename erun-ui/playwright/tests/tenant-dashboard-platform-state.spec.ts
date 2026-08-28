import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// tenant-dashboard-platform-state.spec.ts covers the dashboard's
// platform-readiness states (not-connected, choose-alias, not-signed-in,
// not-enrolled, no-permission), each rendering its own distinct next action
// instead of one generic "sign in again" sentence. See
// tenant-dashboard-refresh.spec.ts for why this stages a throwaway env and
// stubs the RPC layer rather than using the seeded baseline or a real
// backend — the seeded baseline's only cloud alias is AWS-typed, which is
// exactly the configuration this issue exists to stop the dashboard from
// using for the platform.

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

interface StubbedResponse {
  data?: Record<string, unknown>;
  error?: string;
}

// stubRPC intercepts every named method on /__erun_invoke, returning
// whatever the caller's map holds for it at call time — a mutable holder so
// a test can change what a later call returns (e.g. after a stubbed sign-in
// "succeeds").
function stubRPC(
  page: import('@playwright/test').Page,
  responses: Record<string, StubbedResponse>,
): {
  respondWith: (method: string, next: StubbedResponse) => void;
  calls: (method: string) => number;
} {
  const counts: Record<string, number> = {};
  void page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    const stubbed = responses[body.method];
    if (stubbed) {
      counts[body.method] = (counts[body.method] ?? 0) + 1;
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(stubbed.error ? { error: stubbed.error } : { data: stubbed.data }),
      });
      return;
    }
    await route.continue();
  });
  return {
    respondWith: (method, next) => {
      responses[method] = next;
    },
    calls: (method) => counts[method] ?? 0,
  };
}

test.describe('tenant dashboard — platform-readiness states (#1393)', () => {
  test('not-connected renders a Connect action, no tab strip', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('platform-not-connected');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      stubRPC(page, {
        LoadTenantDashboard: { data: { tenant: SEED_TENANT, platformState: 'not-connected' } },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await expect(app.tenantDashboard.notConnectedHeading()).toBeVisible();
      await expect(app.tenantDashboard.connectApiUrlInput()).toBeVisible();
      // The default must be the platform's own apex host regardless of
      // tenant name — SEED_TENANT ('pw') is exactly the shape that used to
      // interpolate into a host that was NXDOMAIN for every tenant but frs.
      await expect(app.tenantDashboard.connectApiUrlInput()).toHaveValue(
        'https://api.erunpaas.com',
      );
      await expect(app.tenantDashboard.tabs()).toHaveCount(0);
      await expect(page.getByRole('button', { name: 'Refresh', exact: true })).toHaveCount(0);
      await page.screenshot({
        path: 'test-results/tenant-dashboard-not-connected-default.png',
      });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('not-connected names the standard host when a custom URL fails to verify', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('platform-not-connected-bad-url');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      stubRPC(page, {
        LoadTenantDashboard: { data: { tenant: SEED_TENANT, platformState: 'not-connected' } },
        ConnectERunPlatform: { error: 'platform discovery failed: dial tcp: lookup: no such host' },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.connectApiUrlInput().fill('https://api.wrong-host.example');
      await app.tenantDashboard.connectButton().click();

      await expect(app.tenantDashboard.connectErrorAlert()).toContainText('no such host');
      await expect(app.tenantDashboard.connectErrorAlert()).toContainText(
        'https://api.erunpaas.com',
      );
      // The failure stays on the panel with the field still editable — no
      // dead end, and the recovery (retype the named default) is still one
      // click away.
      await expect(app.tenantDashboard.connectApiUrlInput()).toBeEditable();
      await expect(app.tenantDashboard.notConnectedHeading()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('choose-alias lists every configured alias as its own choice', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('platform-choose-alias');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      stubRPC(page, {
        LoadTenantDashboard: {
          data: {
            tenant: SEED_TENANT,
            platformState: 'choose-alias',
            platformAliasChoices: ['erun+a@erun', 'erun+b@erun'],
          },
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await expect(app.tenantDashboard.chooseAliasHeading()).toBeVisible();
      await expect(app.tenantDashboard.chooseAliasButton('erun+a@erun')).toBeVisible();
      await expect(app.tenantDashboard.chooseAliasButton('erun+b@erun')).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('not-signed-in renders Log in, names the resolved platform, and recovers on success', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('platform-not-signed-in');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      const stub = stubRPC(page, {
        LoadTenantDashboard: {
          data: {
            tenant: SEED_TENANT,
            platformState: 'not-signed-in',
            platformAlias: 'erun+api.frs-prod.services.erunpaas.com@erun',
            platformUrl: 'https://api.frs-prod.services.erunpaas.com',
          },
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await expect(app.tenantDashboard.notSignedInHeading()).toBeVisible();
      await expect(app.tenantDashboard.platformContactLine()).toContainText(
        'https://api.frs-prod.services.erunpaas.com',
      );
      await expect(app.tenantDashboard.tabs()).toHaveCount(0);

      // A successful sign-in must recover the dashboard on its own — no
      // Refresh click needed ("Smooth, no dead ends").
      stub.respondWith('LoginCloudProvider', {
        data: {
          alias: 'erun+api.frs-prod.services.erunpaas.com@erun',
          provider: 'erun',
          status: 'active',
        },
      });
      stub.respondWith('LoadTenantDashboard', {
        data: {
          tenant: SEED_TENANT,
          platformState: '',
          canCreateReview: true,
          canAdvanceMergeQueue: true,
        },
      });

      await app.tenantDashboard.signInButton().click();
      await app.tenantDashboard.waitForOpen();
      expect(stub.calls('LoadTenantDashboard')).toBeGreaterThanOrEqual(2);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('not-enrolled shows the copyable administrator hand-off, not "sign in again"', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('platform-not-enrolled');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      stubRPC(page, {
        LoadTenantDashboard: {
          data: {
            tenant: SEED_TENANT,
            platformState: 'not-enrolled',
            platformAlias: 'erun+api.frs-prod.services.erunpaas.com@erun',
            platformIssuer: 'https://auth.erunpaas.com',
            platformSubject: 'user-42',
          },
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await expect(app.tenantDashboard.notEnrolledHeading()).toBeVisible();
      await expect(app.tenantDashboard.enrollUsernameInput()).toBeVisible();
      await expect(app.tenantDashboard.tryEnrollButton()).toBeVisible();
      await expect(app.tenantDashboard.enrollAdminCommand()).toContainText(
        'erun platform user enroll',
      );
      await expect(app.tenantDashboard.enrollAdminCommand()).toContainText('user-42');
      await expect(app.tenantDashboard.signInButton()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('no-permission is a plain notice with no dead-end action', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('platform-no-permission');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      stubRPC(page, {
        LoadTenantDashboard: {
          data: {
            tenant: SEED_TENANT,
            platformState: 'no-permission',
            platformAlias: 'erun+api.frs-prod.services.erunpaas.com@erun',
          },
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await expect(app.tenantDashboard.noPermissionHeading()).toBeVisible();
      await expect(app.tenantDashboard.signInButton()).toHaveCount(0);
      await expect(app.tenantDashboard.tryEnrollButton()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // Regression guard: the sidebar's own cloud-alias "Log in" control
  // (Sidebar.PrimaryCloudAliasControl.tsx, dispatching loginPrimaryCloudProvider
  // directly, no dashboard recovery attached) must render exactly as before
  // this change — the seeded baseline's AWS alias is the fixture this issue's
  // fix must never touch.
  test("the sidebar's own cloud-alias login button is unchanged", async ({ app }) => {
    await app.sidebar.openCloudAliasPopover('pw-aws');
    await expect(app.sidebar.cloudAliasPopoverButton('Log in')).toBeVisible();
    await expect(app.sidebar.cloudAliasPopoverButton('Log in')).toBeEnabled();
  });
});
