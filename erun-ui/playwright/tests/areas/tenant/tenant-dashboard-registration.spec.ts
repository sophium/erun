import type { Request, Route } from '@playwright/test';

import { expect, test, waitForSeededRow, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  removeEnvironment,
  seedEnvironment,
  SEED_ENV_ALPHA,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The RPC is stubbed because the harness cannot reach erun-backend-api.

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
      await waitForSeededRow(app, SEED_TENANT, environment);

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

  // A failure must carry a glyph, not colour alone (WCAG 1.4.1).
  test('a failed row states its reason with a glyph, not colour alone', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('registration-failed-reason');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await routeInvoke(page, {
        LoadTenantDashboard: () =>
          registrationDashboardData(environment, {
            environments: [
              {
                environmentId: 'env-1',
                name: 'prod-env',
                type: 'runtime',
                status: 'failed',
                provisionError: 'deploy job did not succeed',
              },
            ],
          }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Registration');

      const reason = app.tenantDashboard.environmentRowAlert('prod-env');
      await expect(reason).toHaveCount(1);
      await expect(reason).toContainText('deploy job did not succeed');
      await expect(reason.locator('svg')).toHaveCount(1);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // DataCell's `truncate` nowrap would ellipsise the reason, so check the
  // computed style.
  test('a failed row wraps a long reason instead of losing it to the cell', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('registration-failed-wrap');
    const reason =
      'run-instances: InsufficientInstanceCapacity - https://console.aws.amazon.com/ec2/home#LaunchInstanceWizard';
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await routeInvoke(page, {
        LoadTenantDashboard: () =>
          registrationDashboardData(environment, {
            environments: [
              {
                environmentId: 'env-1',
                name: 'prod-env',
                type: 'runtime',
                status: 'failed',
                provisionError: reason,
              },
            ],
          }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Registration');

      const alert = app.tenantDashboard.environmentRowAlert('prod-env');
      await expect(alert).toContainText('InsufficientInstanceCapacity');
      const computed = await alert.evaluate((node) => {
        const style = window.getComputedStyle(node);
        return { overflowWrap: style.overflowWrap, whiteSpace: style.whiteSpace };
      });
      expect(computed.overflowWrap).toBe('anywhere');
      expect(computed.whiteSpace).toBe('normal');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // With nothing readable or writable the tab is hidden; the restricted-access
  // note still names the gap.
  test('a caller with nothing readable or writable never sees the tab at all', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('registration-restricted');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

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

  // Preview and register submit the same fields, so the preview describes exactly
  // what register creates.
  test('a full registration path: preview, then register the environment', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('registration-full-path');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      let dashboardLoads = 0;
      let previewCalled = false;
      let registerCalled = false;
      await routeInvoke(page, {
        LoadTenantDashboard: () => {
          dashboardLoads += 1;
          return registrationDashboardData(environment, {
            canRegisterEnvironment: true,
            environments: registerCalled
              ? [{ environmentId: 'env-2', name: 'new-env', type: 'runtime', status: 'registered' }]
              : [],
          });
        },
        PreviewPlatformEnvironment: () => {
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

      await app.tenantDashboard.envNameInput().fill('new-env');
      await app.tenantDashboard.previewEnvironmentButton().click();
      // expect.poll defaults to expect.timeout, not the test deadline.
      await expect.poll(() => previewCalled, withTestBudget()).toBe(true);
      await expect(app.tenantDashboard.activePanel()).toContainText('namespace frs-new-env');
      await expect(app.tenantDashboard.activePanel()).toContainText('Quota ok');

      await app.tenantDashboard.registerEnvironmentButton().click();
      await expect.poll(() => registerCalled, withTestBudget()).toBe(true);

      // Register success re-fetches the dashboard.
      await expect.poll(() => dashboardLoads, withTestBudget()).toBeGreaterThanOrEqual(2);
      await expect(app.tenantDashboard.environmentRow('new-env')).toContainText('registered');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // Adopting a local env prefills the form and previews no deploy; no
  // runtimeVersion or contextId is sent.
  test('putting a local environment on the platform previews and records it without deploying', async ({
    app,
    page,
  }) => {
    interface EnvironmentInputArg {
      adopt?: boolean;
      kubernetesContext?: string;
      runtimeVersion?: string;
    }
    function firstArg(request: Request): EnvironmentInputArg {
      const body = JSON.parse(request.postData() ?? '{}') as { args?: EnvironmentInputArg[] };
      return body.args?.[0] ?? {};
    }
    let previewCalled: EnvironmentInputArg = {};
    let registerCalled: EnvironmentInputArg = {};
    await routeInvoke(page, {
      LoadTenantDashboard: () =>
        registrationDashboardData(SEED_ENV_ALPHA, { canRegisterEnvironment: true }),
      PreviewPlatformEnvironment: (request: Request) => {
        previewCalled = firstArg(request);
        return {
          plan: [
            'adopt: tenant pw',
            'deploy: skipped — adopting an existing environment never starts a deploy',
          ],
          quotaOk: true,
        };
      },
      RegisterPlatformEnvironment: (request: Request) => {
        registerCalled = firstArg(request);
        return {
          kind: 'accepted',
          environment: {
            environmentId: 'env-3',
            name: SEED_ENV_ALPHA,
            type: 'local-agent',
            status: 'registered',
          },
        };
      },
    });

    await app.sidebar.openTenantDashboard(SEED_TENANT);
    await app.tenantDashboard.waitForOpen();
    await app.tenantDashboard.selectTab('Registration');

    await expect(app.tenantDashboard.localEnvironmentsHeading()).toBeVisible();
    await app.tenantDashboard.putOnPlatformButtonFor(SEED_ENV_ALPHA).click();

    await expect(app.tenantDashboard.envNameInput()).toHaveValue(SEED_ENV_ALPHA);
    await expect(app.tenantDashboard.envAdoptToggle()).toBeChecked();
    await expect(app.tenantDashboard.envKubernetesContextInput()).not.toHaveValue('');

    await app.tenantDashboard.previewEnvironmentButton().click();
    await expect.poll(() => previewCalled.adopt, withTestBudget()).toBe(true);
    await expect(app.tenantDashboard.activePanel()).toContainText('deploy: skipped');

    await app.tenantDashboard.recordEnvironmentButton().click();
    await expect.poll(() => registerCalled.adopt, withTestBudget()).toBe(true);
    expect(registerCalled.runtimeVersion).toBeFalsy();
  });

  // A quota cap is an expected outcome and must read as recoverable.
  test('a quota-cap conflict names the recovery instead of failing as an error', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('registration-quota-conflict');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

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

      await app.tenantDashboard.envNameInput().fill('one-too-many');
      await app.tenantDashboard.registerEnvironmentButton().click();

      const recoverable = app.tenantDashboard.recoverableNote('environment quota reached');
      await expect(recoverable).toBeVisible();
      await expect(recoverable).toHaveAttribute('role', 'status');
      await expect(app.tenantDashboard.activePanel().getByRole('alert')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('deleting an environment requires typing its name to confirm', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('registration-delete-confirm');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

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

      await app.tenantDashboard.deleteCancelButtonFor('prod-env').click();
      await expect(confirmInput).toHaveCount(0);
      expect(deleteCalled).toBe(false);

      await app.tenantDashboard.deleteButtonFor('prod-env').click();
      await app.tenantDashboard.deleteConfirmInputFor('prod-env').fill('prod-env');
      await app.tenantDashboard.deleteButtonFor('prod-env').click();

      await expect.poll(() => deleteCalled, withTestBudget()).toBe(true);
      await expect(app.tenantDashboard.environmentRow('prod-env')).toContainText('deleting');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // A read-only caller must be told which write access is missing.
  test('a read-only caller is told which write access is missing, not pointed at absent forms', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('registration-read-only');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await routeInvoke(page, {
        LoadTenantDashboard: () =>
          registrationDashboardData(environment, {
            canCreateContext: false,
            canRegisterEnvironment: false,
          }),
      });

      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Registration');

      const panel = app.tenantDashboard.activePanel();
      await expect(panel).toContainText('registering a cloud context needs additional access');
      await expect(panel).toContainText('registering a hosted environment needs additional access');

      await expect(app.tenantDashboard.previewEnvironmentButton()).toHaveCount(0);
      await expect(app.tenantDashboard.envNameInput()).toHaveCount(0);
      await expect(panel).not.toContainText('Preview a plan above');
      await expect(panel).not.toContainText('Register one below');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
