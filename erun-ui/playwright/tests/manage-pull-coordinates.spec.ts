import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The desktop resolved and passed a private --runtime-image but had no field
// for the secret that image needs, so an environment created from the app went
// ImagePullBackOff with no in-app repair. These cover the two coordinates that
// decide whether the runtime pod can pull at all.

test.describe('manage dialog pull coordinates', () => {
  test('names why a pull secret is needed instead of showing an empty control', async ({ app }) => {
    // No save — the seeded baseline stays untouched.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    // An empty state must read as an explanation, not as a disabled input.
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
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.runtimeRegistryInput().fill('ghcr.io/sophium');
    await app.manageDialog.addPullSecretButton().click();
    await app.manageDialog.pullSecretInput(0).fill('ecr-pull');
    await app.manageDialog.addPullSecretButton().click();
    await app.manageDialog.pullSecretInput(1).fill('ghcr-pull');

    await app.manageDialog.save();
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

  // The repair path the issue is about: a wrong secret is removed and the
  // removal has to stick, or the environment keeps failing after the operator
  // believes they fixed it.
  test('removing a secret clears it rather than leaving a stale one', async ({
    app,
    seededEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.addPullSecretButton().click();
    await app.manageDialog.pullSecretInput(0).fill('wrong-secret');
    await app.manageDialog.save();

    await app.manageDialog.removePullSecretButton(0).click();
    await expect(app.manageDialog.pullSecretInput(0)).toHaveCount(0);
    await app.manageDialog.save();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.pullSecretInput(0)).toHaveCount(0);
    await expect(
      app.manageDialog.locator().getByText('A runtime image in a private registry needs a'),
    ).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
