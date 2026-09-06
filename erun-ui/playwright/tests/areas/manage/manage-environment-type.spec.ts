import { test, expect } from '../../../fixtures/erunApp.js';

// The General tab's "Environment type" is an editable, constrained selector so an
// env whose type was mis-set can be corrected in place; the type drives build/deploy policy.
test.describe('manage dialog environment type selector (#615)', () => {
  test('lights the General dot when changed and persists the corrected type', async ({
    app,
    seededEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.environmentTypeSelect()).toBeVisible();
    await expect
      .poll(() => app.manageDialog.environmentTypeSelectedValue())
      .toContain('Local agent');
    expect(await app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

    await app.manageDialog.chooseEnvironmentType('Remote agent (worktree cloned to PVC)');
    await expect
      .poll(() => app.manageDialog.environmentTypeSelectedValue())
      .toContain('Remote agent');
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(true);

    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

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
