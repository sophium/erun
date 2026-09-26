import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

test.describe('manage dialog container registries', () => {
  test('renders the marked list, adds a row, and surfaces the validation hint', async ({ app }) => {
    // No save — the seeded baseline stays untouched.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    await expect(app.manageDialog.registryInput(0)).toHaveValue('registry.example/test');
    await expect(app.manageDialog.registryRoleCheckbox(0, 'build')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'deploy')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'from')).not.toBeChecked();

    // The role meaning (e.g. "erun build/push target") rides the IconTooltip
    // primitive, not a native `title` (AGENTS.md bans `title` for meaningful UI).
    const buildRoleLabel = app.manageDialog.registryRoleCheckbox(0, 'build').locator('xpath=..');
    await expect(buildRoleLabel).not.toHaveAttribute('title', /.*/);

    // A new row defaults to build+deploy, so a second registry with a host makes two build-marked registries — invalid.
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

    // A deploy-only registry is valid — the image it serves may be published
    // there externally, so no build/to role is forced on it.
    await app.manageDialog.registryRoleCheckbox(0, 'build').click();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'deploy')).toBeChecked();
    await expect(app.manageDialog.registryRoleCheckbox(0, 'build')).not.toBeChecked();
    await expect(app.manageDialog.locator().getByRole('alert')).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The blocking hint is the field's whole inline surface, and it used to state
  // its objection in amber and nothing else -- role and tone, no glyph, so the
  // signal that the save is blocked reached only a reader who can see the
  // colour (WCAG 1.4.1). The vitest suite pins the rendered markup; this pins
  // it in the real webview, on the two properties a stylesheet decides and a
  // string scanner cannot: the glyph an operator actually gets, and the wrap
  // that keeps a long hint inside the dialog instead of widening it.
  test('states a blocked save with a glyph and a hint that wraps', async ({ app }) => {
    // No save — the seeded baseline stays untouched.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    // A new row defaults to build+deploy and the seeded row is already build,
    // so giving the new one a host makes two build-marked registries: blocked.
    await app.manageDialog.addRegistryButton().click();
    await app.manageDialog.registryInput(1).fill('registry.internal/pw');
    await app.page.keyboard.press('Escape');

    const hint = app.manageDialog.locator().getByRole('alert');
    await expect(hint).toHaveCount(1);
    await expect(hint).toContainText('Only one registry can be marked build.');
    await expect(hint.locator('svg')).toHaveCount(1);

    // Asserted on the computed style, because a class that never applies is
    // exactly the failure being guarded against: laying the glyph beside the
    // text makes the hint a flex row, and a flex item's initial
    // `min-width: auto` is its min-content width, so without the shrink the
    // text cannot wrap at all.
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

    // Turn the single seeded registry into a copy-on-deploy setup: registry 1
    // builds and is the copy source; registry 2 is the copy destination the
    // cluster pulls from.
    await app.manageDialog.registryRoleCheckbox(0, 'deploy').click();
    await app.manageDialog.registryRoleCheckbox(0, 'from').click();
    await app.manageDialog.addRegistryButton().click();
    await app.manageDialog.registryInput(1).fill('registry.internal/pw');
    await app.page.keyboard.press('Escape');
    await app.manageDialog.registryRoleCheckbox(1, 'build').click();
    await app.manageDialog.registryRoleCheckbox(1, 'to').click();

    await expect(app.manageDialog.locator().getByRole('alert')).toHaveCount(0);

    await app.manageDialog.save();
    // The manage dialog stays open after save; the changed registry list is a
    // pod-shaping value, so it raises the pending-redeploy banner.
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();

    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    // Every read below is the reopened dialog's own config load -- a value the
    // environment's config file owns, and one `expect(...)` bounds by expect's
    // 10s default rather than by the budget this test declared. The first is the
    // one that proves the reload landed; the rest are then already satisfied.
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
