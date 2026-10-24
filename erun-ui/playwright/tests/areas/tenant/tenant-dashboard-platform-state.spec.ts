import type { Request, Route } from '@playwright/test';

import { artifactPath } from '../../../fixtures/artifacts.js';
import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// Each platform-readiness state renders its own next action. The seeded alias
// is AWS-typed, which the platform must never use, so the RPC is stubbed.

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

interface StubbedResponse {
  data?: Record<string, unknown>;
  error?: string;
}

// stubRPC answers each method from a mutable map read at call time.
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
      await waitForSeededRow(app, SEED_TENANT, environment);

      stubRPC(page, {
        LoadTenantDashboard: { data: { tenant: SEED_TENANT, platformState: 'not-connected' } },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await expect(app.tenantDashboard.notConnectedHeading()).toBeVisible();
      await expect(app.tenantDashboard.connectApiUrlInput()).toBeVisible();
      // The default is the platform apex host, never derived from the tenant name.
      await expect(app.tenantDashboard.connectApiUrlInput()).toHaveValue(
        'https://api.erunpaas.com',
      );
      await expect(app.tenantDashboard.tabs()).toHaveCount(0);
      // Every tab is replaced, so the body must not single out some as failing.
      await expect(app.tenantDashboard.notConnectedBody()).toHaveText(
        "This tenant isn't connected to a hosted erun platform yet, so none of this dashboard's tabs can load.",
      );
      await expect(page.getByRole('button', { name: 'Refresh', exact: true })).toHaveCount(0);
      await page.screenshot({
        path: artifactPath('test-results/tenant-dashboard-not-connected-default.png'),
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
      await waitForSeededRow(app, SEED_TENANT, environment);

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
      // The field stays editable after a failure, so retrying is one click away.
      await expect(app.tenantDashboard.connectApiUrlInput()).toBeEditable();
      await expect(app.tenantDashboard.notConnectedHeading()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('choose-alias lists every configured alias as its own choice', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('platform-choose-alias');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

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
      await waitForSeededRow(app, SEED_TENANT, environment);

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

      // A successful sign-in must recover the dashboard without a Refresh click.
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
      await waitForSeededRow(app, SEED_TENANT, environment);

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

  // The platform refuses self-enroll for a non-enrolled caller; that must render
  // as the administrator hand-off, not the raw error.
  test('the self-enroll refusal renders the administrator hand-off, not the raw error', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('platform-enroll-refused');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      const stub = stubRPC(page, {
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
      await app.tenantDashboard.enrollUsernameInput().fill('jane');

      stub.respondWith('EnrollERunPlatformUser', {
        error:
          'You do not have permission to enroll yourself in this tenant. Ask an administrator to run the command below, or request an invitation.',
      });
      await app.tenantDashboard.tryEnrollButton().click();

      await expect(app.tenantDashboard.enrollErrorAlert()).toContainText(
        'do not have permission to enroll yourself',
      );
      for (const leak of ['http 401', 'http 403', '/v1/users', 'NOT_ENROLLED']) {
        await expect(app.tenantDashboard.enrollErrorAlert()).not.toContainText(leak);
      }
      await expect(app.tenantDashboard.enrollAdminCommand()).toBeVisible();
      await expect(page.getByRole('button', { name: 'Request an invitation' })).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('no-permission is a plain notice with no dead-end action', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('platform-no-permission');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

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

  // Only the platform's own tenant name can tell whether the rows are this
  // tenant's; on mismatch none of the stubbed rows may render.
  test('tenant-mismatch names whose rows these are and renders none of them', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('platform-tenant-mismatch');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      stubRPC(page, {
        LoadTenantDashboard: {
          data: {
            tenant: SEED_TENANT,
            platformState: 'tenant-mismatch',
            platformUrl: 'https://api.frs-prod.services.erunpaas.com',
            user: {
              tenantId: 'tenant-1',
              tenantName: 'erun',
              userId: 'user-1',
              username: 'reader',
            },
            reviews: [{ reviewId: 'r1', name: 'another tenant review', status: 'OPEN' }],
          },
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);

      await expect(app.tenantDashboard.tenantMismatchHeading()).toBeVisible();
      await expect(app.tenantDashboard.tenantMismatchBody()).toContainText('erun');
      await expect(app.tenantDashboard.tenantMismatchBody()).toContainText(SEED_TENANT);
      // The tab strip and panels are replaced, so no other tenant's rows show.
      await expect(app.tenantDashboard.tabs()).toHaveCount(0);
      await expect(page.getByText('another tenant review')).toHaveCount(0);
      await expect(app.tenantDashboard.platformTenantLine()).toContainText('erun');
      await expect(app.tenantDashboard.connectApiUrlInput()).toBeVisible();
      await expect(app.tenantDashboard.connectButton()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test("the sidebar's own cloud-alias login button is unchanged", async ({ app }) => {
    await app.sidebar.openCloudAliasPopover('pw-aws');
    await expect(app.sidebar.cloudAliasPopoverButton('Log in')).toBeVisible();
    await expect(app.sidebar.cloudAliasPopoverButton('Log in')).toBeEnabled();
  });
});
