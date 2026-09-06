import { test, expect } from '../../../fixtures/erunApp.js';

// The Manage dialog Runtime tab's "Ignore project build.sh" toggle changes how a
// redeploy rebuilds the runtime image, so saving it raises the Pending-redeploy
// banner.
test.describe('manage dialog disable-build-script toggle (#533)', () => {
  test('lights the Runtime dot, raises the redeploy banner, and persists', async ({
    app,
    seededEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const checkbox = app.manageDialog.disableBuildScriptCheckbox();
    await expect(checkbox).not.toBeChecked();
    expect(await app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);

    await checkbox.click();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(true);

    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);
    await expect(app.manageDialog.redeployBanner()).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await expect(app.manageDialog.disableBuildScriptCheckbox()).toBeChecked();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
