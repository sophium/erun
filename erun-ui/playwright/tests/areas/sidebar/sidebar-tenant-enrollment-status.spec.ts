import type { Request, Route } from '@playwright/test';

import { artifactPath } from '../../../fixtures/artifacts.js';
import { expect, test } from '../../../fixtures/erunApp.js';
import {
  addERunCloudProviderAlias,
  removeTenant,
  seedEnvironment,
  seedTenant,
  SEED_CLOUD_ALIAS,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

interface StubbedResponse {
  data?: unknown;
  error?: string;
}

// stubRPC answers one method from a mutable holder, optionally gated. Other
// calls use route.fallback(), not continue(), so stubs for several methods chain.
function stubRPC(
  page: import('@playwright/test').Page,
  method: string,
  initial: StubbedResponse,
): {
  respondWith: (next: StubbedResponse) => void;
  calls: () => number;
  hold: () => void;
  release: () => void;
} {
  let current = initial;
  let calls = 0;
  let gate: Promise<void> | undefined;
  let releaseGate: (() => void) | undefined;
  void page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== method) {
      await route.fallback();
      return;
    }
    calls += 1;
    if (gate) {
      await gate;
    }
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify(current.error ? { error: current.error } : { data: current.data }),
    });
  });
  return {
    respondWith: (next) => {
      current = next;
    },
    calls: () => calls,
    hold: () => {
      gate = new Promise((resolve) => {
        releaseGate = resolve;
      });
    },
    release: () => {
      releaseGate?.();
      gate = undefined;
    },
  };
}

// The seeded baseline has no 'erun' alias, so its envs are local-only at once;
// other states are stubbed, with page.clock driving the 30s poll.

test.describe('sidebar tenant enrollment status icon', () => {
  test('renders a hollow ring for a tenant with local environments and no platform connection', async ({
    app,
  }) => {
    const icon = app.sidebar.tenantEnrollmentStatus(SEED_TENANT);
    await expect(icon).toBeVisible();
    await expect(icon).toHaveAttribute('data-enrollment-state', 'local-only');
    // The state must be named in words, not colour alone (WCAG 1.4.1).
    await expect(
      app.page.getByRole('button', { name: `${SEED_TENANT} is not on the hosted platform yet` }),
    ).toBeVisible();
  });

  // Real Go path, no stub. The sidebar glyph collapses both failure shapes to
  // local-only, so this reads the dashboard heading that distinguishes them.
  test('the dashboard reports not-connected, never sign-in for an unselected alias, when a tenant selected only an AWS alias', async ({
    app,
  }) => {
    const tenant = uniqueEnvironmentName('erun-1955');
    const environment = uniqueEnvironmentName('env');
    const erunAlias = `${uniqueEnvironmentName('erun-alias')}@erun`;
    const restoreCloudProvider = addERunCloudProviderAlias(
      erunAlias,
      'http://127.0.0.1:1/unreachable',
    );
    seedTenant(
      tenant,
      environment,
      `cloudprovideraliases:\n  - ${SEED_CLOUD_ALIAS}\n` +
        `primarycloudprovideralias: ${SEED_CLOUD_ALIAS}\n`,
    );
    seedEnvironment(tenant, environment);
    try {
      await app.reloadEnvironments();
      const icon = app.sidebar.tenantEnrollmentStatus(tenant);
      await expect(icon).toHaveAttribute('data-enrollment-state', 'local-only');

      await app.sidebar.openTenantDashboard(tenant);
      await expect(app.tenantDashboard.notConnectedHeading()).toBeVisible();
      await expect(app.tenantDashboard.notSignedInHeading()).toHaveCount(0);
    } finally {
      removeTenant(tenant);
      restoreCloudProvider();
    }
  });

  test('does not render for a tenant with zero local environments', async ({ app }) => {
    // A zero-environment tenant has no row; wait on the seeded row's round trip,
    // then assert the icon is absent.
    const tenant = uniqueEnvironmentName('zero-env-tenant');
    seedTenant(tenant, 'none');
    try {
      await app.reloadEnvironments();
      await app.sidebar.tenantEnrollmentStatus(SEED_TENANT).waitFor({ state: 'visible' });
      await expect(app.sidebar.tenantRow(tenant)).toHaveCount(0);
      await expect(app.sidebar.tenantEnrollmentStatus(tenant)).toHaveCount(0);
    } finally {
      removeTenant(tenant);
    }
  });

  test('clicking the icon does not toggle the row collapse state', async ({ app }) => {
    const expandedBefore = await app.sidebar.isTenantExpanded(SEED_TENANT);
    await app.sidebar.openTenantEnrollmentStatusPopover(SEED_TENANT);
    await expect(app.sidebar.tenantEnrollmentStatusPopover()).toBeVisible();
    expect(await app.sidebar.isTenantExpanded(SEED_TENANT)).toBe(expandedBefore);
  });

  test('the local-only popover offers a request-an-invitation and a sign-in action', async ({
    app,
  }) => {
    await app.sidebar.openTenantEnrollmentStatusPopover(SEED_TENANT);
    const popover = app.sidebar.tenantEnrollmentStatusPopover();
    await expect(popover.getByRole('button', { name: 'Request an invitation' })).toBeVisible();
    await expect(popover.getByRole('button', { name: 'Sign in' })).toBeVisible();
  });

  test('renders identically legibly in light and dark theme', async ({ app, page }) => {
    const icon = app.sidebar.tenantEnrollmentStatus(SEED_TENANT);
    await expect(icon).toBeVisible();
    await page.screenshot({
      path: artifactPath('test-results/sidebar-tenant-enrollment-status-local-only-light.png'),
      animations: 'disabled',
    });
    await page.evaluate(() => {
      document.documentElement.classList.add('dark');
    });
    await expect(icon).toBeVisible();
    await page.screenshot({
      path: artifactPath('test-results/sidebar-tenant-enrollment-status-local-only-dark.png'),
      animations: 'disabled',
    });
  });

  test('renders nothing before the first status resolves -- never a guessed state', async ({
    app,
    page,
  }) => {
    const tenant = uniqueEnvironmentName('enrollment-loading');
    const environment = uniqueEnvironmentName('env');
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      const stub = stubRPC(page, 'ListTenantPlatformEnrollmentStatuses', {
        data: [{ tenant, state: 'local-only' }],
      });
      stub.hold();

      await app.reloadEnvironments();
      await app.sidebar.tenantRow(tenant).first().waitFor({ state: 'visible' });
      // Until the status resolves the icon must render nothing.
      await expect(app.sidebar.tenantEnrollmentStatus(tenant)).toHaveCount(0);

      stub.release();
      await expect(app.sidebar.tenantEnrollmentStatus(tenant)).toHaveAttribute(
        'data-enrollment-state',
        'local-only',
      );
    } finally {
      removeTenant(tenant);
    }
  });

  test('renders as unknown, not local-only, when the status genuinely could not be determined, both themes', async ({
    app,
    page,
  }) => {
    const tenant = uniqueEnvironmentName('enrollment-unknown');
    const environment = uniqueEnvironmentName('env');
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      stubRPC(page, 'ListTenantPlatformEnrollmentStatuses', {
        data: [{ tenant, state: 'unknown' }],
      });

      await app.reloadEnvironments();
      const icon = app.sidebar.tenantEnrollmentStatus(tenant);
      await expect(icon).toHaveAttribute('data-enrollment-state', 'unknown');
      await expect(
        app.page.getByRole('button', {
          name: `${tenant}'s platform enrollment status could not be checked`,
        }),
      ).toBeVisible();

      await page.screenshot({
        path: artifactPath('test-results/sidebar-tenant-enrollment-status-unknown-light.png'),
        animations: 'disabled',
      });
      await page.evaluate(() => {
        document.documentElement.classList.add('dark');
      });
      await expect(icon).toBeVisible();
      await page.screenshot({
        path: artifactPath('test-results/sidebar-tenant-enrollment-status-unknown-dark.png'),
        animations: 'disabled',
      });
    } finally {
      removeTenant(tenant);
    }
  });

  test('renders a mismatch, naming the tenant and host that answered, when the platform resolves another tenant', async ({
    app,
    page,
  }) => {
    const tenant = uniqueEnvironmentName('enrollment-mismatch');
    const environment = uniqueEnvironmentName('env');
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      stubRPC(page, 'ListTenantPlatformEnrollmentStatuses', {
        data: [
          {
            tenant,
            state: 'tenant-mismatch',
            platformHost: 'api.erunpaas.com',
            platformTenant: 'someone-else',
          },
        ],
      });

      await app.reloadEnvironments();
      const icon = app.sidebar.tenantEnrollmentStatus(tenant);
      await expect(icon).toHaveAttribute('data-enrollment-state', 'tenant-mismatch');
      // Names the host that actually answered, never a fixed hostname.
      await expect(
        app.page.getByRole('button', {
          name: `${tenant}'s connection to api.erunpaas.com belongs to the tenant someone-else`,
        }),
      ).toBeVisible();

      await app.sidebar.openTenantEnrollmentStatusPopover(tenant);
      const popover = app.sidebar.tenantEnrollmentStatusPopover();
      await expect(popover).toContainText('Different tenant on api.erunpaas.com');
      await expect(popover).toContainText('someone-else');
      // The dashboard owns the connect form; the row does not re-implement it.
      await expect(popover.getByRole('button', { name: 'Open the dashboard' })).toBeVisible();
    } finally {
      removeTenant(tenant);
    }
  });

  // 'unknown' is a round-trip failure, so it must keep polling like 'pending'.
  test('unknown keeps polling rather than going silent, and recovers once the platform answers again', async ({
    app,
    page,
  }) => {
    const tenant = uniqueEnvironmentName('enrollment-unknown-recovers');
    const environment = uniqueEnvironmentName('env');
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      const stub = stubRPC(page, 'ListTenantPlatformEnrollmentStatuses', {
        data: [{ tenant, state: 'unknown' }],
      });
      await page.clock.install();

      await app.reloadEnvironments();
      const icon = app.sidebar.tenantEnrollmentStatus(tenant);
      await expect(icon).toHaveAttribute('data-enrollment-state', 'unknown');

      const callsBeforeWait = stub.calls();
      stub.respondWith({ data: [{ tenant, state: 'enrolled' }] });
      await page.clock.fastForward(30_000);

      // The stubbed route is a real async round trip, so poll the counter.
      await expect.poll(() => stub.calls()).toBeGreaterThan(callsBeforeWait);
      await expect(icon).toHaveAttribute('data-enrollment-state', 'enrolled');
    } finally {
      removeTenant(tenant);
    }
  });

  test('renders nothing, not a guessed state, when the status round trip fails outright', async ({
    app,
    page,
  }) => {
    const tenant = uniqueEnvironmentName('enrollment-rpc-fail');
    const environment = uniqueEnvironmentName('env');
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      stubRPC(page, 'ListTenantPlatformEnrollmentStatuses', { error: 'boom' });

      await app.reloadEnvironments();
      await app.sidebar.tenantRow(tenant).first().waitFor({ state: 'visible' });
      // A transport failure leaves no data, so the icon stays absent.
      await expect(app.sidebar.tenantEnrollmentStatus(tenant)).toHaveCount(0);
    } finally {
      removeTenant(tenant);
    }
  });

  // local-only does not poll, so sign-in success must invalidate the status.
  test('the local-only popover sign-in action turns the icon enrolled without a restart', async ({
    app,
    page,
  }) => {
    const tenant = uniqueEnvironmentName('enrollment-sign-in');
    const environment = uniqueEnvironmentName('env');
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      const enrollment = stubRPC(page, 'ListTenantPlatformEnrollmentStatuses', {
        data: [{ tenant, state: 'local-only' }],
      });
      stubRPC(page, 'LoadTenantDashboard', {
        data: { tenant, platformState: 'not-signed-in', platformAlias: 'erun+api.example@erun' },
      });
      stubRPC(page, 'LoginCloudProvider', {
        data: { alias: 'erun+api.example@erun', provider: 'erun', status: 'active' },
      });

      await app.reloadEnvironments();
      const icon = app.sidebar.tenantEnrollmentStatus(tenant);
      await expect(icon).toHaveAttribute('data-enrollment-state', 'local-only');
      await expect(
        app.page.getByRole('button', { name: `${tenant} is not on the hosted platform yet` }),
      ).toBeVisible();

      await app.sidebar.openTenantEnrollmentStatusPopover(tenant);
      await app.sidebar
        .tenantEnrollmentStatusPopover()
        .getByRole('button', { name: 'Sign in' })
        .click();
      await app.tenantDashboard.notSignedInHeading().waitFor({ state: 'visible' });

      enrollment.respondWith({ data: [{ tenant, state: 'enrolled' }] });
      await app.tenantDashboard.signInButton().click();

      await expect(icon).toHaveAttribute('data-enrollment-state', 'enrolled');
      await expect(
        app.page.getByRole('button', { name: `${tenant} is enrolled in the hosted platform` }),
      ).toBeVisible();
    } finally {
      removeTenant(tenant);
    }
  });

  test('pending -> declined -> enrolled: the icon changes live as the state does, and an approval re-fetches', async ({
    app,
    page,
  }) => {
    const tenant = uniqueEnvironmentName('enrollment-transitions');
    const environment = uniqueEnvironmentName('env');
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      const stub = stubRPC(page, 'ListTenantPlatformEnrollmentStatuses', {
        data: [{ tenant, state: 'pending' }],
      });
      // Installed before the row mounts so the poll's setInterval uses the fake clock.
      await page.clock.install();

      await app.reloadEnvironments();
      const icon = app.sidebar.tenantEnrollmentStatus(tenant);
      await expect(icon).toHaveAttribute('data-enrollment-state', 'pending');

      stub.respondWith({ data: [{ tenant, state: 'declined', declineReason: 'not now' }] });
      await page.clock.fastForward(30_000);
      await expect(icon).toHaveAttribute('data-enrollment-state', 'declined');
      await app.sidebar.openTenantEnrollmentStatusPopover(tenant);
      await expect(app.sidebar.tenantEnrollmentStatusPopover()).toContainText('not now');

      stub.respondWith({ data: [{ tenant, state: 'enrolled' }] });
      await page.clock.fastForward(30_000);
      await expect(icon).toHaveAttribute('data-enrollment-state', 'enrolled');
      // The approval fires a one-shot notification in the message centre.
      await app.titlebar.openMessageCenter('success');
      await expect(
        app.titlebar.messageCenterRow(`Approved -- you're enrolled in ${tenant}.`),
      ).toBeVisible();
    } finally {
      removeTenant(tenant);
    }
  });
});
