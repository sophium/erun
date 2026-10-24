import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

test.describe('manage dialog container registries', () => {
  test('renders the marked list, adds a row, and surfaces the validation hint', async ({ app }) => {
    // No save: the seeded baseline stays untouched.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    await expect(app.manageDialog.registryInput(0)).toHaveValue('registry.example/test');
    await expect(app.manageDialog.registryRoleCheckbox(0, 'build')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'deploy')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'from')).not.toBeChecked();

    // AGENTS.md bans native `title` for meaningful UI.
    const buildRoleLabel = app.manageDialog.registryRoleCheckbox(0, 'build').locator('xpath=..');
    await expect(buildRoleLabel).not.toHaveAttribute('title', /.*/);

    // A new row defaults to build+deploy, so a second hosted registry makes two build registries: invalid.
    await app.manageDialog.addRegistryButton().click();
    await app.manageDialog.registryInput(1).fill('registry.internal/pw');
    await app.page.keyboard.press('Escape');
    await expect(
      app.manageDialog.locator().getByText('Only one registry can be marked build.'),
    ).toBeVisible();

    await app.manageDialog.removeRegistryButton(1).click();
    await expect(
      app.manageDialog.locator().getByText('Only one registry can be marked build.'),
    ).toBeHidden();

    // A deploy-only registry is valid: its image may be published there externally.
    await app.manageDialog.registryRoleCheckbox(0, 'build').click();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'deploy')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'build')).not.toBeChecked();
    await expect(app.manageDialog.locator().getByRole('alert')).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('states a blocked save with a glyph and a hint that wraps', async ({ app }) => {
    // No save: the seeded baseline stays untouched.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.addRegistryButton().click();
    await app.manageDialog.registryInput(1).fill('registry.internal/pw');
    await app.page.keyboard.press('Escape');

    const hint = app.manageDialog.locator().getByRole('alert');
    await expect(hint).toHaveCount(1);
    await expect(hint).toContainText('Only one registry can be marked build.');
    await expect(hint.locator('svg')).toHaveCount(1);

    // Computed style, because a flex item's `min-width: auto` stops the text wrapping without the shrink.
    const layout = await hint.evaluate((node) => {
      const text = node.querySelector('span');
      return {
        display: window.getComputedStyle(node).display,
        overflowWrap: window.getComputedStyle(node).overflowWrap,
        textMinWidth: text ? window.getComputedStyle(text).minWidth : '',
      };
    });
    expect(layout.display).toBe('flex');
    expect(layout.overflowWrap).toBe('anywhere');
    expect(layout.textMinWidth).toBe('0px');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('saves a build/from/to/deploy list and reloads it', async ({ app, seededEnv }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.registryRoleCheckbox(0, 'deploy').click();
    await app.manageDialog.registryRoleCheckbox(0, 'from').click();
    await app.manageDialog.addRegistryButton().click();
    await app.manageDialog.registryInput(1).fill('registry.internal/pw');
    await app.page.keyboard.press('Escape');
    await app.manageDialog.registryRoleCheckbox(1, 'build').click();
    await app.manageDialog.registryRoleCheckbox(1, 'to').click();

    await expect(app.manageDialog.locator().getByRole('alert')).toHaveCount(0);

    await app.manageDialog.save();
    // A registry change is pod-shaping, so it raises the pending-redeploy banner.
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    // The first read proves the reload landed; the rest are then already satisfied.
    await expect(app.manageDialog.registryInput(0)).toHaveValue(
      'registry.example/test',
      withTestBudget(),
    );
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
