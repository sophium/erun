import { test, expect } from '../fixtures/erunApp.js';

// The Manage dialog's General tab "Environment type" is now an
// editable, constrained selector (local-agent | remote-agent | runtime) rather
// than a read-only label, so an env whose type was mis-set (e.g. resolved to
// "runtime" on what is really a remote-agent env) can be corrected in place.
// The type drives build/deploy policy, so the change lights the General tab's
// unsaved-changes dot and round-trips through the Go load/save path. Uses a
// per-test seeded env (type: local-agent) and a real save+reload, the
// manage-disable-build-script technique.
test.describe('manage dialog environment type selector (#615)', () => {
  test('lights the General dot when changed and persists the corrected type', async ({
    app,
    seededEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    // General is the default tab; the selector shows the seeded local-agent type.
    await expect(app.manageDialog.environmentTypeSelect()).toBeVisible();
    await expect
      .poll(() => app.manageDialog.environmentTypeSelectedValue())
      .toContain('Local agent');
    expect(await app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

    // Correcting the type to remote-agent reflects in the draft and lights the
    // General tab's unsaved-changes dot (a visible affordance for the edit).
    await app.manageDialog.chooseEnvironmentType('Remote agent (worktree cloned to PVC)');
    await expect
      .poll(() => app.manageDialog.environmentTypeSelectedValue())
      .toContain('Remote agent');
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(true);

    // Saving persists it; the dot clears.
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

    // The corrected type round-trips through the Go load/save path: reopening
    // the dialog shows remote-agent.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await expect
      .poll(() => app.manageDialog.environmentTypeSelectedValue())
      .toContain('Remote agent');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
