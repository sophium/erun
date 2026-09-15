import type { Request, Route } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';
import type { GlobalConfigDialog } from '../../../pages/GlobalConfigDialog.js';

// stubERunConfig answers the config read, so a spec can stage the machine's own
// gateway defaults without touching the developer's real user settings.
function stubERunConfig(
  page: import('@playwright/test').Page,
  config: Record<string, unknown>,
): void {
  void page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== 'LoadERunConfig') {
      await route.continue();
      return;
    }
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        data: { cloudProviders: [], cloudContexts: [], ...config },
      }),
    });
  });
}

// stubGatewayModels answers the gateway model-list read with a fixed catalog, so
// the picker can be exercised without a live gateway. It stands in for the
// backend method, so it returns that method's own result shape — the parsed
// {id, displayName, context} — not the gateway's wire shape, which the Go side
// parses and covers in its own tests.
function stubGatewayModels(
  page: import('@playwright/test').Page,
  models: Record<string, unknown>[],
): void {
  void page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== 'LoadGatewayModels') {
      await route.continue();
      return;
    }
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: models }),
    });
  });
}

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

    // The gateway is chosen, so the URL field only appears for a self-hosted
    // address; an unconfigured install shows the selector at Not configured.
    await expect(app.globalConfigDialog.openRouterGatewayTrigger()).toContainText('Not configured');
    await expect(app.globalConfigDialog.openRouterBaseURLInput()).toBeHidden();
    await expect(app.globalConfigDialog.openRouterSecretInput()).toBeVisible();
    await expect(app.globalConfigDialog.openRouterSecretKeyInput()).toBeVisible();
    // An unconfigured install offers no rows until the operator adds one.
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(0);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('opens pre-filled from this machine’s own gateway', async ({ app, page }) => {
    // A catalog that is not yet configured starts from what this machine's
    // Claude Code already runs, so an endpoint and model configured once are not
    // typed again. Nothing is stored by opening it — the operator still saves.
    stubERunConfig(page, {
      defaultTenant: SEED_TENANT,
      openRouterDefaults: {
        baseUrl: 'https://openrouter.ai/api',
        model: 'deepseek/deepseek-v4.1-flash',
        context: 1048576,
      },
    });

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await expect(app.globalConfigDialog.openRouterGatewayTrigger()).toContainText(
      'https://openrouter.ai/api',
    );
    await expect(app.globalConfigDialog.openRouterModelIdInput(0)).toHaveValue(
      'deepseek/deepseek-v4.1-flash',
    );
    await expect(app.globalConfigDialog.openRouterModelContextInput(0)).toHaveValue('1048576');

    // Cancelling leaves nothing stored, which is the contract for a pre-fill.
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

  test('picks a model from the gateway and fills in the window it reports', async ({
    app,
    page,
  }) => {
    // The gateway publishes what it serves, so the id is chosen rather than
    // typed — and the window comes with the pick, because retyping a figure the
    // gateway already reported is exactly the kind of entry this avoids. The
    // second entry carries no display name, so both label shapes are exercised.
    stubGatewayModels(page, [
      { id: 'deepseek/deepseek-v4.1-flash', displayName: 'DeepSeek V4.1 Flash', context: 1048576 },
      { id: 'openai/gpt-6-astra', context: 1050000 },
    ]);

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');
    await app.globalConfigDialog.openRouterAddModelButton().click();
    // Before the list is loaded the row accepts a typed id.
    await expect(app.globalConfigDialog.openRouterModelIdInput(0)).toBeVisible();

    await app.globalConfigDialog.loadGatewayModels();
    await expect(app.globalConfigDialog.openRouterModelSelect(0)).toBeVisible();
    await app.globalConfigDialog.selectOpenRouterModel(0, 'openai/gpt-6-astra');

    await expect(app.globalConfigDialog.openRouterModelSelect(0)).toContainText(
      'openai/gpt-6-astra',
    );
    await expect(app.globalConfigDialog.openRouterModelContextInput(0)).toHaveValue('1050000');

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
    await expect(app.globalConfigDialog.openRouterGatewayTrigger()).toContainText(
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
