import { test, expect } from '../../../fixtures/erunApp.js';
import type { GlobalConfigDialog } from '../../../pages/GlobalConfigDialog.js';

// restoreCatalog clears everything these specs set. They mutate the suite's
// global config rather than an environment, and the worker's backend is shared
// with every other spec, so leaving a gateway configured would swap every later
// spec's selectable models for the catalog's.
async function restoreCatalog(dialog: GlobalConfigDialog): Promise<void> {
  await dialog.setOpenRouterBaseURL('');
  await dialog.openRouterSecretInput().fill('');
  // Remove from the end: each removal renumbers the rows after it.
  let remaining = await dialog.openRouterModelRows().count();
  while (remaining > 0) {
    await dialog.openRouterRemoveModelButton(remaining - 1).click();
    remaining -= 1;
  }
  await dialog.save();
  await dialog.waitForClosed();
}

// The erun-level gateway catalog is edited in ERun settings and consumed by
// every environment's AI tab, so the observable contract is: the fields render,
// a model id the launch would drop is refused at entry, and what is saved comes
// back on reopen.
test.describe('erun-level gateway catalog', () => {
  test('renders the gateway fields for an unconfigured install', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    await expect(app.globalConfigDialog.openRouterBaseURLInput()).toBeVisible();
    await expect(app.globalConfigDialog.openRouterSecretInput()).toBeVisible();
    await expect(app.globalConfigDialog.openRouterSecretKeyInput()).toBeVisible();
    // An unconfigured install offers no rows until the operator adds one.
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(0);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('refuses a model id the launch would silently drop', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    await app.globalConfigDialog.openRouterAddModelButton().click();
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(1);
    await app.globalConfigDialog.openRouterModelIdInput(0).fill('a b; rm -rf /');
    await expect(
      app.globalConfigDialog
        .locator()
        .getByText('A model id may contain only letters, digits, and . _ : / -'),
    ).toBeVisible();

    // A well-formed id clears the rejection, so the rule is the token shape and
    // not merely "newly typed text".
    await app.globalConfigDialog.openRouterModelIdInput(0).fill('deepseek/deepseek-v4.1-flash');
    await expect(
      app.globalConfigDialog
        .locator()
        .getByText('A model id may contain only letters, digits, and . _ : / -'),
    ).toBeHidden();

    await restoreCatalog(app.globalConfigDialog);
  });

  test('saves the catalog and shows it again on reopen', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');
    await app.globalConfigDialog.setOpenRouterCredential({ secret: 'pw-claude-gateway' });
    await app.globalConfigDialog.addOpenRouterModel({
      id: 'deepseek/deepseek-v4.1-flash',
      context: 1048576,
    });
    await app.globalConfigDialog.save();
    await app.globalConfigDialog.waitForClosed();

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await expect(app.globalConfigDialog.openRouterBaseURLInput()).toHaveValue(
      'https://openrouter.ai/api',
    );
    await expect(app.globalConfigDialog.openRouterSecretInput()).toHaveValue('pw-claude-gateway');
    await expect(app.globalConfigDialog.openRouterModelIdInput(0)).toHaveValue(
      'deepseek/deepseek-v4.1-flash',
    );
    await expect(app.globalConfigDialog.openRouterModelContextInput(0)).toHaveValue('1048576');

    await restoreCatalog(app.globalConfigDialog);
  });
});
