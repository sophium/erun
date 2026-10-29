import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

test.describe('manage dialog mount-source toggle (#736)', () => {
  test('reveals the repo URL, raises the redeploy banner, and persists', async ({
    app,
    seededRuntimeEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const toggle = app.manageDialog.mountSourceCheckbox();
    await expect(toggle).toBeVisible();
    await expect(toggle).not.toBeChecked();
    await expect(app.manageDialog.repoURLInput()).toHaveCount(0);
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);

    await toggle.click();
    await expect(toggle).toBeChecked();
    const url = app.manageDialog.repoURLInput();
    await expect(url).toBeVisible();
    await url.fill('https://github.com/sophium/erun.git');
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
    await expect(app.manageDialog.mountSourceCheckbox()).toBeChecked(withTestBudget());
    await expect(app.manageDialog.repoURLInput()).toHaveValue(
      'https://github.com/sophium/erun.git',
    );

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('is absent for a non-runtime (agent) env', async ({ app, seededEnv }) => {
    // Runtime-only; the build-script toggle still rendering confirms the tab.
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.disableBuildScriptCheckbox()).toBeVisible();
    await expect(app.manageDialog.mountSourceCheckbox()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
