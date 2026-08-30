import { expect, test } from '../fixtures/erunApp.js';
import {
  removeTenant,
  seedTenant,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// sidebar-tenant-enrollment-status covers the sidebar's per-tenant platform-
// enrollment status icon: a third row-kind status glyph, shown once a
// tenant has at least one local environment and no matching platform
// registration.
//
// The seeded `pw` baseline (fixtures/seedRoot.ts) has no configured 'erun'-
// type cloud provider alias at all (only an aws and a cloudflare alias), so
// every one of its envs resolves to 'local-only' on the very first query --
// no poll wait needed, since ListTenantPlatformEnrollmentStatuses's initial
// fetch already reports it.
//
// pending/declined/enrolled all require a real platform round trip (a
// verified bearer, an actual invite-requests row) the headless harness's aws-
// only stubs cannot produce, and there is no established pattern in this
// suite for stubbing a Wails method's response the way
// src/app/platformSignInRecovery.test.ts's stubWailsBridge does at the
// vitest/node:test level (that seam is a plain object assigned before the
// React app boots, not something reachable once a real headless erun-app
// process is already serving window.go.main.App over the HTTP+SSE bridge).
// That state-classification logic (local-only/pending/declined/enrolled, and
// the poll-gate/transition-detection logic riding on top of it) is instead
// covered by:
//   - erun-ui/tenant_platform_invite_requests_test.go (tenantPlatformEnrollmentStatus)
//   - erun-ui/frontend/src/app/tenantEnrollmentPoll.test.ts (the pure poll-gate
//     and transition helpers)
// This spec locks down the one invariant the harness *can* reach end-to-end:
// the icon's rendering, its accessible name, and that it never hijacks the
// row's own collapse click.

test.describe('sidebar tenant enrollment status icon', () => {
  test('renders a hollow ring for a tenant with local environments and no platform connection', async ({
    app,
  }) => {
    const icon = app.sidebar.tenantEnrollmentStatus(SEED_TENANT);
    await expect(icon).toBeVisible();
    await expect(icon).toHaveAttribute('data-enrollment-state', 'local-only');
    // The accessible name must name the state in words, never colour alone
    // (WCAG 1.4.1) -- checked via getByRole so this also proves the control
    // is exposed as a real button, not a decorative element.
    await expect(
      app.page.getByRole('button', { name: `${SEED_TENANT} is not on erunpaas.com yet` }),
    ).toBeVisible();
  });

  test('does not render for a tenant with zero local environments', async ({ app }) => {
    const tenant = uniqueEnvironmentName('zero-env-tenant');
    seedTenant(tenant, 'none');
    try {
      await expect(async () => {
        await app.reloadEnvironments();
        await app.sidebar.tenantRow(tenant).first().waitFor({ state: 'visible', timeout: 2_000 });
      }).toPass({ timeout: 30_000 });

      await expect(app.sidebar.tenantDashboardButton(tenant)).toBeVisible();
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
      path: 'test-results/sidebar-tenant-enrollment-status-local-only-light.png',
      animations: 'disabled',
    });
    await page.evaluate(() => {
      document.documentElement.classList.add('dark');
    });
    await expect(icon).toBeVisible();
    await page.screenshot({
      path: 'test-results/sidebar-tenant-enrollment-status-local-only-dark.png',
      animations: 'disabled',
    });
  });
});
