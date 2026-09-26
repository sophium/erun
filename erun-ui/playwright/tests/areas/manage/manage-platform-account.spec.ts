import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

// The Manage dialog Runtime tab's "Platform account" toggle binds the env's
// runtime ServiceAccount to cluster-admin so in-pod platform Terraform (the
// cluster edge) and component installs can manage cluster-scoped resources.
// It is deploy-relevant (saving raises the Pending-redeploy banner) and — unlike
// the runtime-only Mount source toggle — renders for every environment type.
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
    // A one-shot read compares once and gives up; the dot's own state is the
    // dialog's to settle, so it is polled on this test's clock instead.
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);

    await toggle.click();
    await expect(toggle).toBeChecked();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(true);

    await app.manageDialog.save();
    // The dot clearing is this save's own round-trip answer, so it waits on the
    // budget the test declared rather than expect's 10s default.
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);
    // Deploy-relevant change → the pending-redeploy banner tells the operator the
    // grant takes effect on the next deploy (visibility of system status).
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible();

    // Reopen: the grant persisted to the env config.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    // The reopened dialog's own config load is what answers this, so it carries
    // the clock this test declared rather than expect's 10s default.
    await expect(app.manageDialog.platformAccountCheckbox()).toBeChecked(withTestBudget());

    // Turning it back off persists too (reconciles both ways).
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
    // The clear persisted to the config, so the third open's own load is what
    // this read waits on -- on this test's clock, not expect's 10s default.
    await expect(app.manageDialog.platformAccountCheckbox()).not.toBeChecked(withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('renders for a non-runtime (agent) env too', async ({ app, seededEnv }) => {
    // The grant is env-type agnostic, so — unlike Mount source, which is
    // runtime-only — the toggle must render on an agent env's Runtime tab.
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.platformAccountCheckbox()).toBeVisible();
    // Sanity: the runtime-only Mount source toggle is absent here, confirming the
    // platform-account toggle's presence is not just "all runtime controls show".
    await expect(app.manageDialog.mountSourceCheckbox()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
