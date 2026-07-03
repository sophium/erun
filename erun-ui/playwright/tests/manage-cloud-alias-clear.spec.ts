import { test, expect } from '../fixtures/erunApp.js';
import { SEED_CLOUD_ALIAS, SEED_ENV_BETA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Regression: the Manage dialog's cloud-alias dropdown could not be cleared
// once set. A "— None —" entry now unlinks the env. The test cancels instead
// of saving so the shared seeded config stays untouched.
test.describe('manage dialog cloud alias clear', () => {
  test('a configured cloud alias can be cleared via "— None —"', async ({ app }) => {
    // Open via the keyboard path: the row's edit button is
    // pointer-events-none until the row is hovered/selected/focused, and a
    // hover would open the row tooltip and intercept a mouse click.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_BETA);
    await app.manageDialog.waitForOpen();
    expect(await app.manageDialog.cloudAliasSelectVisible()).toBe(true);
    await expect.poll(() => app.manageDialog.cloudAliasSelectedValue()).toBe(SEED_CLOUD_ALIAS);

    await app.manageDialog.openCloudAliasOptions();
    await expect(app.manageDialog.cloudAliasNoneOption()).toBeVisible();

    await app.manageDialog.cloudAliasNoneOption().click();
    await expect.poll(() => app.manageDialog.cloudAliasSelectedValue()).toBe('Select cloud alias');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
