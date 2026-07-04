import { test, expect } from '../fixtures/erunApp.js';

// The Manage dialog Runtime tab's "Mount source code" toggle is a runtime-only
// opt-in: it flips the env's worktree onto a PVC the pod clones at the deployed
// release ref, so saving it raises the Pending-redeploy banner. The git remote
// field appears only once the toggle is on.
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
    // The URL field stays hidden until the toggle is on (recognition over recall).
    await expect(app.manageDialog.repoURLInput()).toHaveCount(0);
    expect(await app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);

    await toggle.click();
    await expect(toggle).toBeChecked();
    const url = app.manageDialog.repoURLInput();
    await expect(url).toBeVisible();
    await url.fill('https://github.com/sophium/erun.git');
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(true);

    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);
    await expect(app.manageDialog.redeployBanner()).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await expect(app.manageDialog.mountSourceCheckbox()).toBeChecked();
    await expect(app.manageDialog.repoURLInput()).toHaveValue(
      'https://github.com/sophium/erun.git',
    );

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('is absent for a non-runtime (agent) env', async ({ app, seededEnv }) => {
    // The toggle is runtime-only; an agent env already carries source, so the
    // control must not render on its Runtime tab (the disable-build-script toggle
    // still does, confirming we are on the right tab).
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.disableBuildScriptCheckbox()).toBeVisible();
    await expect(app.manageDialog.mountSourceCheckbox()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
