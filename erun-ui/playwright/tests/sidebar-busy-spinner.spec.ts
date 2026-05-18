import { expect, test } from '../fixtures/erunApp.js';

// sidebar-busy-spinner covers the new running-command spinner that appears
// next to an env name when an activity-queue entry for that env is in
// 'running' state (deploy / init / sshd-init / doctor / build / push /
// release). Activity-queue entries are populated by real terminal sessions
// firing activity events; the headless harness has no portable way to
// stage a running deploy per playwright/AGENTS.md. The spec therefore
// locks the closest reachable invariant: an env that has no live activity
// queued against it must NOT carry a spinner in steady state. The
// positive flow is covered by the existing sidebar spec
// "opening an environment surfaces status feedback" (which exercises the
// same BusyRowSpinner via the isOpening path) plus the
// deriveEnvironmentRow logic in Sidebar.helpers.ts, which selects the
// busy state and aria-label.

test.describe('sidebar busy spinner', () => {
  test('quiet env rows show no spinner', async ({ app, page }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);

    // The BusyRowSpinner mounts with role="status" and a per-env
    // accessible label. An idle env should expose no such role on its
    // row. We restrict the query to the sidebar to avoid colliding with
    // unrelated role="status" surfaces (e.g., terminal banners).
    const sidebar = page.locator('aside').first();
    const spinners = sidebar.getByRole('status');
    await expect(spinners).toHaveCount(0);
  });

  test('navigating away clears any in-flight spinner', async ({ app, page }) => {
    // openSelection's previous incarnation kept its markEnvOpening flag
    // until the underlying StartSession resolved. On a cold EC2 open
    // that could take ~60s, so a user who clicked env A and then env B
    // mid-flight ended up with a stale spinner on A *and* a stale
    // "Opening A..." status banner overwriting the env they were
    // actually looking at. The fix in this commit dispatches
    // resetEnvOpening at the top of each click and gates the post-
    // StartSession promotion behind isCurrentSelection. Lock the
    // observable settle state: after clicking two different envs back
    // to back, the sidebar must surface at most one spinner, and any
    // sidebar spinner that does linger must be on the currently
    // selected env.
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(1);

    await app.sidebar.openEnvironment(tenant, envs[0]!);
    await app.sidebar.openEnvironment(tenant, envs[1]!);

    // Give the second click a moment to land its prepareOpenSelection
    // (which dispatches resetEnvOpening synchronously) and let any
    // first-click spinner clear. The assertion below uses toHaveCount
    // with auto-retry so the test is not racing on a precise timing.
    const sidebar = page.locator('aside').first();
    const spinners = sidebar.getByRole('status');
    await expect(spinners).toHaveCount(0, { timeout: 5_000 });
  });
});
