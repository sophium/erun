import { test, expect } from '../../../fixtures/erunApp.js';

// A host env has no pod: it gets its own badge and never renders as a pod that failed to start.
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
    // HOST and LOCAL badges are mutually exclusive.
    expect(
      await app.sidebar.hasLocalBadge(tenant, environment),
      `Local badge must not also render for ${tenant} / ${environment}`,
    ).toBe(false);
    expect(await app.sidebar.rowHasHostSuffix(tenant, environment)).toBe(true);
    expect(await app.sidebar.rowHasLocalSuffix(tenant, environment)).toBe(false);

    // A host env is never running or stopped, so it has no open/close dot.
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
