import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

test.describe('manage dialog pull coordinates', () => {
  test('names why a pull secret is needed instead of showing an empty control', async ({ app }) => {
    // No save: the seeded baseline stays untouched.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    await expect(
      app.manageDialog.locator().getByText('A runtime image in a private registry needs a'),
    ).toBeVisible();
    await expect(app.manageDialog.pullSecretInput(0)).toHaveCount(0);
    await expect(app.manageDialog.addPullSecretButton()).toBeEnabled();

    await expect(app.manageDialog.runtimeRegistryInput()).toHaveValue('');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('saves both coordinates and reloads them', async ({ app, seededEnv }) => {
    // Two dialog opens, each with its own toPass retry, can exhaust the default 30s.
    test.setTimeout(60_000);
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.runtimeRegistryInput().fill('ghcr.io/sophium');
    await app.manageDialog.addPullSecretButton().click();
    await app.manageDialog.pullSecretInput(0).fill('ecr-pull');
    await app.manageDialog.addPullSecretButton().click();
    await app.manageDialog.pullSecretInput(1).fill('ghcr-pull');

    await app.manageDialog.save();
    // Confirms the save round-tripped before the reopen reads it back.
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.runtimeRegistryInput()).toHaveValue('ghcr.io/sophium');
    await expect(app.manageDialog.pullSecretInput(0)).toHaveValue('ecr-pull');
    await expect(app.manageDialog.pullSecretInput(1)).toHaveValue('ghcr-pull');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('removing a secret clears it rather than leaving a stale one', async ({
    app,
    seededEnv,
  }) => {
    // Two dialog opens, each with its own toPass retry, can exhaust the default 30s.
    test.setTimeout(60_000);
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.addPullSecretButton().click();
    await app.manageDialog.pullSecretInput(0).fill('wrong-secret');
    await app.manageDialog.save();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('General'), withTestBudget())
      .toBe(false);

    await app.manageDialog.removePullSecretButton(0).click();
    await expect(app.manageDialog.pullSecretInput(0)).toHaveCount(0, withTestBudget());
    await app.manageDialog.save();
    // Confirms the save round-tripped before the reopen reads it back.
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('General'), withTestBudget())
      .toBe(false);
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.pullSecretInput(0)).toHaveCount(0, withTestBudget());
    await expect(
      app.manageDialog.locator().getByText('A runtime image in a private registry needs a'),
    ).toBeVisible(withTestBudget());

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
