import { test, expect } from '../../../fixtures/erunApp.js';

// A host environment has no pod and no cluster at all — it must render like
// any other environment, with its own distinct badge, never as a pod that
// failed to start (root AGENTS.md "Smooth, Seamless, No Dead Ends"; issue
// #1380). This mirrors sidebar-local-badge.spec.ts's shape but for the badge
// that must NOT collapse into "Local" even though a host env is also local by
// worktree location.
test.describe('sidebar HOST badge', () => {
  test('a host environment shows the Host badge, the (host) suffix, and no pod-shaped status dot', async ({
    app,
    seededHostEnv,
  }) => {
    const { tenant, environment } = seededHostEnv;

    await expect
      .poll(() => app.sidebar.hasHostBadge(tenant, environment), {
        message: `Host badge for ${tenant} / ${environment}`,
      })
      .toBe(true);
    // A host env is also "local" by worktree location, but must never show
    // the LOCAL pill too — the two badges are mutually exclusive.
    expect(
      await app.sidebar.hasLocalBadge(tenant, environment),
      `Local badge must not also render for ${tenant} / ${environment}`,
    ).toBe(false);
    expect(await app.sidebar.rowHasHostSuffix(tenant, environment)).toBe(true);
    expect(await app.sidebar.rowHasLocalSuffix(tenant, environment)).toBe(false);

    // "Not stopped, it simply is": no pod-shaped open/close status dot, since
    // a host env is never running or stopped.
    await expect(app.sidebar.envOpenDot(tenant, environment)).toHaveCount(0);
  });

  test('the Manage dialog reports the Host type', async ({ app, seededHostEnv }) => {
    const { tenant, environment } = seededHostEnv;

    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await expect.poll(() => app.manageDialog.envTypeFieldValue()).toMatch(/^Host/);
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
