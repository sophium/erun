import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

test.describe('manage dialog platform-account toggle (#804)', () => {
  test('toggles, raises the redeploy banner, and persists on a runtime env', async ({
    app,
    seededRuntimeEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const toggle = app.manageDialog.platformAccountCheckbox();
    await expect(toggle).toBeVisible();
    await expect(toggle).not.toBeChecked();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);

    await toggle.click();
    await expect(toggle).toBeChecked();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(true);

    await app.manageDialog.save();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await expect(app.manageDialog.platformAccountCheckbox()).toBeChecked(withTestBudget());

    await app.manageDialog.platformAccountCheckbox().click();
    await expect(app.manageDialog.platformAccountCheckbox()).not.toBeChecked();
    await app.manageDialog.save();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await expect(app.manageDialog.platformAccountCheckbox()).not.toBeChecked(withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('renders for a non-runtime (agent) env too', async ({ app, seededEnv }) => {
    // The grant is env-type agnostic, unlike the runtime-only Mount source toggle.
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.platformAccountCheckbox()).toBeVisible();
    await expect(app.manageDialog.mountSourceCheckbox()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
