import { test, expect } from '../fixtures/erunApp.js';

// The Manage dialog Runtime tab's "Ignore project build.sh" toggle
// (EnvConfig.disableBuildScript). It is an editable field (lights the Runtime
// tab's unsaved-changes dot) that changes how a redeploy rebuilds the runtime
// image (build.sh vs docker contexts), so saving it raises the Pending-redeploy
// banner. It round-trips through the Go load/save path so a saved value reloads
// checked. Uses a per-test seeded env and a real save+reload (the
// manage-container-registries technique).
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

    // Editing the toggle lights the Runtime dot (it is an editable field).
    await checkbox.click();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(true);

    // Saving it raises the Pending-redeploy banner — it changes how a redeploy
    // rebuilds the image, so the running env owes a redeploy to apply it.
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);
    await expect(app.manageDialog.redeployBanner()).toBeVisible();

    // The saved value round-trips through the Go load/save path: reopening the
    // dialog shows it checked.
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
