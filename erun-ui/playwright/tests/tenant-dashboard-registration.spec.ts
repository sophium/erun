import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import {
  removeEnvironment,
  seedEnvironment,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// The Registration tab closes the gap TenantPlatformState.tsx's
// readiness machine stops short of: an operator authenticated and enrolled
// still has no desktop path to register a tenant/environment on the hosted
// platform. Like tenant-dashboard-permissions.spec.ts, this stubs the
// __erun_invoke RPC boundary rather than a real erun-backend-api, which the
// inert harness deliberately cannot reach; the Go side that resolves this
// data is covered by erun-ui/tenant_platform_registration_test.go.

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

interface RegistrationFixture {
  contexts?: unknown[];
  environments?: unknown[];
  canCreateContext?: boolean;
  canRegisterEnvironment?: boolean;
  canPreviewProvision?: boolean;
  canDeployEnvironment?: boolean;
  canStopEnvironment?: boolean;
  canDeleteEnvironment?: boolean;
}

function registrationDashboardData(
  environment: string,
  registration: RegistrationFixture,
): Record<string, unknown> {
  return {
    tenant: SEED_TENANT,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    user: { tenantId: 't1', userId: 'u1', username: 'operator', roles: ['Admin'] },
    panels: [{ tab: 'registration' }],
    canCreateReview: false,
    canAdvanceMergeQueue: false,
    canOverrideMergeQueue: false,
    canCreateContext: registration.canCreateContext ?? false,
    canRegisterEnvironment: registration.canRegisterEnvironment ?? false,
    canPreviewProvision: registration.canPreviewProvision ?? false,
    canDeployEnvironment: registration.canDeployEnvironment ?? false,
    canStopEnvironment: registration.canStopEnvironment ?? false,
    canDeleteEnvironment: registration.canDeleteEnvironment ?? false,
    contexts: registration.contexts ?? [],
    environments: registration.environments ?? [],
  };
}

async function routeInvoke(
  page: import('@playwright/test').Page,
  handlers: Record<string, (request: Request) => unknown>,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    const handler = handlers[body.method];
    if (handler) {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: handler(request) }),
      });
      return;
    }
    await route.continue();
  });
}

test.describe('tenant dashboard — Registration tab', () => {
  test('shows the two registries and what is already registered', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('registration-visible');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      await routeInvoke(page, {
        LoadTenantDashboard: () =>
          registrationDashboardData(environment, {
            canDeployEnvironment: true,
            canStopEnvironment: true,
            canDeleteEnvironment: true,
            contexts: [{ contextId: 'ctx-1', name: 'prod', provider: 'aws', status: 'running' }],
            environments: [
              {
                environmentId: 'env-1',
                name: 'prod-env',
                type: 'runtime',
                status: 'running',
                deployedVersion: '1.2.3',
              },
            ],
          }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Registration');

      await expect(app.tenantDashboard.twoRegistriesNotice()).toBeVisible();
      await expect(app.tenantDashboard.contextsHeading()).toBeVisible();
      await expect(app.tenantDashboard.activePanel()).toContainText('prod');
      await expect(app.tenantDashboard.environmentsHeading()).toBeVisible();
      await expect(app.tenantDashboard.environmentRow('prod-env')).toContainText('running');
      await expect(app.tenantDashboard.environmentRow('prod-env')).toContainText('1.2.3');
      await expect(app.tenantDashboard.deployButtonFor('prod-env')).toBeVisible();
      await expect(app.tenantDashboard.stopButtonFor('prod-env')).toBeVisible();
      await expect(app.tenantDashboard.deleteButtonFor('prod-env')).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // A caller with neither list readable nor either write grantable has
  // nothing this tab could show, so — exactly like every other panel whose
  // read is restricted — the tab itself does not render; the missing access
  // is still named in the shared restricted-access note above the strip.
  test('a caller with nothing readable or writable never sees the tab at all', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('registration-restricted');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      await routeInvoke(page, {
        LoadTenantDashboard: () => ({
          ...registrationDashboardData(environment, {}),
          panels: [
            { tab: 'users' },
            { tab: 'registration', restricted: 'GET /v1/contexts' },
            { tab: 'audit' },
          ],
        }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();

      await expect(app.tenantDashboard.tab('Registration')).toHaveCount(0);
      await expect(app.tenantDashboard.restrictedAccessNote()).toContainText('GET /v1/contexts');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The full path: preview provisioning (never a write), then register the
  // environment it previewed, and see it land in the list — rule #3 (a
  // preview precedes the register action) plus the round trip that proves
  // the desktop can now do what used to dead-end at the CLI.
  test('a full registration path: preview provisioning, then register the environment', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('registration-full-path');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      let dashboardLoads = 0;
      let previewCalled = false;
      let registerCalled = false;
      await routeInvoke(page, {
        LoadTenantDashboard: () => {
          dashboardLoads += 1;
          return registrationDashboardData(environment, {
            canPreviewProvision: true,
            canRegisterEnvironment: true,
            environments: registerCalled
              ? [{ environmentId: 'env-2', name: 'new-env', type: 'runtime', status: 'registered' }]
              : [],
          });
        },
        PreviewPlatformProvision: () => {
          previewCalled = true;
          return { plan: ['resolve tenant frs', 'namespace frs-new-env'], quotaOk: true };
        },
        RegisterPlatformEnvironment: () => {
          registerCalled = true;
          return {
            kind: 'accepted',
            environment: {
              environmentId: 'env-2',
              name: 'new-env',
              type: 'runtime',
              status: 'registered',
            },
          };
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Registration');
      await expect(app.tenantDashboard.environmentsEmptyState()).toBeVisible();

      await app.tenantDashboard.previewEnvNameInput().fill('new-env');
      await app.tenantDashboard.previewProvisioningButton().click();
      await expect.poll(() => previewCalled).toBe(true);
      await expect(app.tenantDashboard.activePanel()).toContainText('namespace frs-new-env');
      await expect(app.tenantDashboard.activePanel()).toContainText('Quota ok');

      await app.tenantDashboard.registerEnvNameInput().fill('new-env');
      await app.tenantDashboard.registerEnvironmentButton().click();
      await expect.poll(() => registerCalled).toBe(true);

      // The register success re-fetches the dashboard, so the new
      // environment appears without a manual refresh.
      await expect.poll(() => dashboardLoads).toBeGreaterThanOrEqual(2);
      await expect(app.tenantDashboard.environmentRow('new-env')).toContainText('registered');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // A quota cap is an expected, actionable outcome, not a fault — it must
  // read as a recoverable state (rule #5), not a raw error.
  test('a quota-cap conflict names the recovery instead of failing as an error', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('registration-quota-conflict');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      await routeInvoke(page, {
        LoadTenantDashboard: () =>
          registrationDashboardData(environment, { canRegisterEnvironment: true }),
        RegisterPlatformEnvironment: () => ({
          kind: 'conflict',
          message: 'environment quota reached: this tenant already has 3 of 3 environments',
        }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Registration');

      await app.tenantDashboard.registerEnvNameInput().fill('one-too-many');
      await app.tenantDashboard.registerEnvironmentButton().click();

      const recoverable = app.tenantDashboard.recoverableNote('environment quota reached');
      await expect(recoverable).toBeVisible();
      await expect(recoverable).toHaveAttribute('role', 'status');
      // Never rendered as a fault: no alert carries this text.
      await expect(app.tenantDashboard.activePanel().getByRole('alert')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // An unrecoverable delete requires typing the object's name to confirm
  // (root AGENTS.md Design-Language Decision Record), the same boundary
  // every other destructive dashboard action gets.
  test('deleting an environment requires typing its name to confirm', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('registration-delete-confirm');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

      let deleteCalled = false;
      await routeInvoke(page, {
        LoadTenantDashboard: () =>
          registrationDashboardData(environment, {
            canDeleteEnvironment: true,
            environments: [
              deleteCalled
                ? { environmentId: 'env-3', name: 'prod-env', type: 'runtime', status: 'deleting' }
                : { environmentId: 'env-3', name: 'prod-env', type: 'runtime', status: 'running' },
            ],
          }),
        DeletePlatformEnvironment: () => {
          deleteCalled = true;
          return {
            kind: 'accepted',
            environment: {
              environmentId: 'env-3',
              name: 'prod-env',
              type: 'runtime',
              status: 'deleting',
            },
          };
        },
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Registration');

      await app.tenantDashboard.deleteButtonFor('prod-env').click();
      const confirmInput = app.tenantDashboard.deleteConfirmInputFor('prod-env');
      await expect(confirmInput).toBeVisible();
      const confirmDelete = app.tenantDashboard.deleteButtonFor('prod-env');
      await expect(confirmDelete).toBeDisabled();

      await confirmInput.fill('the wrong name');
      await expect(confirmDelete).toBeDisabled();

      await confirmInput.fill('prod-env');
      await expect(confirmDelete).toBeEnabled();

      // Cancel backs out without deleting anything.
      await app.tenantDashboard.deleteCancelButtonFor('prod-env').click();
      await expect(confirmInput).toHaveCount(0);
      expect(deleteCalled).toBe(false);

      await app.tenantDashboard.deleteButtonFor('prod-env').click();
      await app.tenantDashboard.deleteConfirmInputFor('prod-env').fill('prod-env');
      await app.tenantDashboard.deleteButtonFor('prod-env').click();

      await expect.poll(() => deleteCalled).toBe(true);
      await expect(app.tenantDashboard.environmentRow('prod-env')).toContainText('deleting');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
