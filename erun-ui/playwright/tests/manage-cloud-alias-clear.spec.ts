import { test, expect } from '../fixtures/erunApp.js';
import { SEED_CLOUD_ALIAS, SEED_ENV_BETA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Regression: the env Manage dialog's Cloud alias dropdown had no
// way to clear a selection once set; it only offered configured aliases. The
// fix always adds a selectable "— None —" entry whenever at least one alias is
// configured, mapping to an empty cloudProviderAlias (env renders "Not
// linked"). Verified here without saving, so the seeded config is untouched.
//
// The seeded baseline stages exactly the starting state: beta links the
// configured pw-aws alias (backed by the inert aws stub), so the select must
// render with the alias selected and clearing it must return the placeholder.
test.describe('manage dialog cloud alias clear', () => {
  test('a configured cloud alias can be cleared via "— None —"', async ({ app }) => {
    // Open via the keyboard path: the row's edit button is
    // pointer-events-none until the row is hovered/selected/focused, and a
    // hover would open the row tooltip and intercept a mouse click.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_BETA);
    await app.manageDialog.waitForOpen();
    expect(await app.manageDialog.cloudAliasSelectVisible()).toBe(true);
    // The env starts with the seeded alias selected.
    await expect.poll(() => app.manageDialog.cloudAliasSelectedValue()).toBe(SEED_CLOUD_ALIAS);

    // The clear option is always offered when aliases exist.
    await app.manageDialog.openCloudAliasOptions();
    await expect(app.manageDialog.cloudAliasNoneOption()).toBeVisible();

    // Selecting it clears the draft value back to the placeholder, the
    // observable signal that cloudProviderAlias became empty. Cancel without
    // saving so the seeded config is not mutated.
    await app.manageDialog.cloudAliasNoneOption().click();
    await expect.poll(() => app.manageDialog.cloudAliasSelectedValue()).toBe('Select cloud alias');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
