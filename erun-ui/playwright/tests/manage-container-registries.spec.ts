import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Issue #527 — the env Manage dialog's General tab edits the project's MARKED
// container-registry list: rows of a registry host plus build/from/to/deploy
// role toggles, with add/remove and a live validation hint mirroring the
// backend marker invariants. The seeded local-agent env carries a single
// registry (registry.example/test, build+deploy), so the editor must render it
// as one row with build+deploy checked.
test.describe('manage dialog container registries', () => {
  test('renders the marked list, adds a row, and surfaces the validation hint', async ({ app }) => {
    // No save — the seeded baseline stays untouched.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    // The seeded single registry renders as one row, build+deploy marked.
    await expect(app.manageDialog.registryInput(0)).toHaveValue('registry.example/test');
    await expect(app.manageDialog.registryRoleCheckbox(0, 'build')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'deploy')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'from')).not.toBeChecked();

    // Add a second registry (defaults to build+deploy) and give it a host —
    // now two registries are marked build, which is invalid.
    await app.manageDialog.addRegistryButton().click();
    await app.manageDialog.registryInput(1).fill('registry.internal/pw');
    await app.page.keyboard.press('Escape');
    await expect(
      app.manageDialog.locator().getByText('Only one registry can be marked build.'),
    ).toBeVisible();

    // Removing the added row clears the conflict.
    await app.manageDialog.removeRegistryButton(1).click();
    await expect(
      app.manageDialog.locator().getByText('Only one registry can be marked build.'),
    ).toBeHidden();

    // A deploy-only registry is valid — the image it serves may be published
    // there externally, so no build/to role is forced on it (#527 follow-up).
    await app.manageDialog.registryRoleCheckbox(0, 'build').click();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'deploy')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'build')).not.toBeChecked();
    await expect(app.manageDialog.locator().getByRole('alert')).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('saves a build/from/to/deploy list and reloads it', async ({ app, seededEnv }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    // Turn the single seeded registry into a copy-on-deploy setup: registry 1
    // builds and is the copy source; registry 2 is the copy destination the
    // cluster pulls from.
    await app.manageDialog.registryRoleCheckbox(0, 'deploy').click(); // build+from on registry 1
    await app.manageDialog.registryRoleCheckbox(0, 'from').click();
    await app.manageDialog.addRegistryButton().click();
    await app.manageDialog.registryInput(1).fill('registry.internal/pw');
    await app.page.keyboard.press('Escape');
    await app.manageDialog.registryRoleCheckbox(1, 'build').click(); // to+deploy on registry 2
    await app.manageDialog.registryRoleCheckbox(1, 'to').click();

    // A valid list raises no hint.
    await expect(app.manageDialog.locator().getByRole('alert')).toHaveCount(0);

    await app.manageDialog.save();
    // The manage dialog stays open after save; the changed registry list is a
    // pod-shaping value, so it raises the pending-redeploy banner (#460).
    await expect(app.manageDialog.redeployBanner()).toBeVisible();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();

    // Reopen: the saved marked list round-trips (read back from project config
    // for the local-agent env).
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.registryInput(0)).toHaveValue('registry.example/test');
    await expect(app.manageDialog.registryRoleCheckbox(0, 'build')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'from')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'deploy')).not.toBeChecked();
    await expect(app.manageDialog.registryInput(1)).toHaveValue('registry.internal/pw');
    await expect(app.manageDialog.registryRoleCheckbox(1, 'to')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(1, 'deploy')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(1, 'build')).not.toBeChecked();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
