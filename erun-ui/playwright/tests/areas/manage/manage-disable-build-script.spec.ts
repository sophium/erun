import { test, expect } from '../../../fixtures/erunApp.js';

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
    await app.manageDialog.waitForRedeployBanner();
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

  // A host env builds in its own directory, so this toggle must survive the host
  // env's pod/cluster-control notice.
  test('renders for a host environment, which has no pod but does build', async ({
    app,
    seededHostEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededHostEnv.tenant, seededHostEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const checkbox = app.manageDialog.disableBuildScriptCheckbox();
    await expect(checkbox).toBeVisible();
    await expect(checkbox).not.toBeChecked();

    await checkbox.click();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(true);
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(seededHostEnv.tenant, seededHostEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await expect(app.manageDialog.disableBuildScriptCheckbox()).toBeChecked();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
