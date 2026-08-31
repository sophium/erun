import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

interface StubbedResponse {
  data?: unknown;
  error?: string;
}

// stubRPC intercepts one named method on /__erun_invoke, returning whatever
// the caller's holder currently points at -- a mutable holder so a test can
// change what the *next* call returns (driving pending -> declined ->
// enrolled across live poll cycles), and an optional gate so a test can
// assert on the pre-response state before releasing it. Mirrors
// tenant-dashboard-platform-state.spec.ts's stubRPC, scoped to this file per
// this suite's own no-shared-harness convention.
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
      await route.continue();
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
// pending/declined/enrolled/unknown are driven live below by stubbing
// ListTenantPlatformEnrollmentStatuses at the __erun_invoke RPC boundary
// (the same seam tenant-dashboard-platform-state.spec.ts already uses for
// LoadTenantDashboard) rather than a real platform round trip, and
// page.clock fastForwards tenantEnrollmentPoll.ts's real 30s poll interval
// so the pending->declined->enrolled walk doesn't cost real wall-clock time.
// The per-tenant *classification* logic (which Go branch produces which
// state) is covered by erun-ui/tenant_platform_invite_requests_test.go; the
// pure poll-gate/transition helpers are covered by
// erun-ui/frontend/src/app/tenantEnrollmentPoll.test.ts. This spec is the one
// place that proves the icon itself actually changes as the underlying state
// does, live, through a real render.

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
    // A zero-environment tenant never gets a sidebar row at all --
    // state_handlers.go's stateFromListResult skips it outright ("a tenant
    // with none has nothing to host yet" per Sidebar.TenantGroup.tsx's own
    // comment), so there is no row to wait for becoming visible; the
    // regression this guards is a future change teaching the icon to render
    // independently of the row itself. Wait on the environments-changed
    // round trip settling (via a tenant that does have one, the seeded
    // baseline) rather than a fixed sleep, then assert absence.
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
      // The row exists, but the enrollment status round trip has not
      // resolved yet -- the icon must render nothing, never one of the
      // definite states, while that is true.
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
        path: 'test-results/sidebar-tenant-enrollment-status-unknown-light.png',
        animations: 'disabled',
      });
      await page.evaluate(() => {
        document.documentElement.classList.add('dark');
      });
      await expect(icon).toBeVisible();
      await page.screenshot({
        path: 'test-results/sidebar-tenant-enrollment-status-unknown-dark.png',
        animations: 'disabled',
      });
    } finally {
      removeTenant(tenant);
    }
  });

  // The bug this guards: nextEnrollmentPollingInterval used to return 0 for
  // 'unknown' (treating a genuine round-trip failure as terminal, same as
  // 'local-only' and 'enrolled'), so a transient outage never recovered on
  // its own. It must keep polling exactly like 'pending'/'declined'.
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

      // Proves the poll actually fired again (not merely that the icon
      // happens to match) -- a terminal 'unknown' would never issue this
      // second call at all. fastForward only guarantees the fake timer fired;
      // the route interception that increments `calls` is a real async
      // round trip through Playwright's own routing layer, so poll for it
      // rather than reading the counter the instant fastForward resolves.
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
      // A transport-level failure leaves the query with no data at all --
      // the icon stays absent, the same as the pre-load state, rather than
      // falling back to a confident wrong answer.
      await expect(app.sidebar.tenantEnrollmentStatus(tenant)).toHaveCount(0);
    } finally {
      removeTenant(tenant);
    }
  });

  // The one test that drives the whole loop live: pending -> declined ->
  // enrolled, through the real 30s poll (fastForwarded, never a real wait),
  // proving the icon itself changes rather than sitting on a stale state
  // until the operator happens to reopen the app.
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
      // Installed before this tenant's row (and its own enrollment poll)
      // ever mounts, so the setInterval tenantEnrollmentPoll.ts creates once
      // it sees a non-terminal state is the fake one fastForward controls.
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
      // The approval transition (leaving a non-terminal state and landing on
      // enrolled) fires a one-shot notification -- proof the row does not
      // silently sit on a stale "pending" once an operator has approved it
      // elsewhere; the icon re-fetched and moved on its own.
      await expect(
        page.getByRole('status').filter({ hasText: `Approved -- you're enrolled in ${tenant}.` }),
      ).toBeVisible();
    } finally {
      removeTenant(tenant);
    }
  });
});
