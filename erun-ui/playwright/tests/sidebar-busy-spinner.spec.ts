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
});
