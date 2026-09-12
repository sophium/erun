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

  // A host env has no pod and no cluster, so the Runtime tab replaces its pod
  // and cluster controls with the "not available for this environment type"
  // notice. This toggle is not one of them: erun build resolves its Docker and
  // release contexts from any env whose builds run in that env's own directory,
  // a host env included, and this dialog is the only desktop surface that sets
  // it. Losing it here would leave the setting reachable only by hand-editing
  // config.yaml.
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
