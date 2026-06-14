import { test, expect } from '../fixtures/erunApp.js';

// #533 — the Manage dialog Runtime tab's "Ignore project build.sh" toggle
// (EnvConfig.disableBuildScript). It is an editable field (lights the Runtime
// tab's unsaved-changes dot) that never reaches the running pod (so saving it
// must not raise the Pending-redeploy banner), and it round-trips through the
// Go load/save path so a saved value reloads checked. Uses a per-test seeded
// env and a real save+reload (the manage-container-registries technique).
test.describe('manage dialog disable-build-script toggle (#533)', () => {
  test('lights the Runtime dot without a redeploy banner and persists', async ({
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

    // Saving it never raises the Pending-redeploy banner (never reaches the pod).
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);
    await expect(app.manageDialog.redeployBanner()).toHaveCount(0);

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
