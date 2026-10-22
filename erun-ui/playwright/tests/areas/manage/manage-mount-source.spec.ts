import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

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
    // A one-shot read compares once and gives up; the dot's own state is the
    // dialog's to settle, so it is polled on this test's clock instead.
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
    // The dot clearing is this save's own round-trip answer, so it waits on the
    // budget the test declared rather than expect's 10s default.
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
    // The reopened dialog's own config load is what answers these, so the first
    // read carries this test's clock rather than expect's 10s default.
    await expect(app.manageDialog.mountSourceCheckbox()).toBeChecked(withTestBudget());
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
