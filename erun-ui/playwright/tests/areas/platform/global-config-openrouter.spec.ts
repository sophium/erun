import type { Page, Request, Route } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';
import type { GlobalConfigDialog } from '../../../pages/GlobalConfigDialog.js';

// Stages gateway defaults without touching the developer's real user settings.
function stubERunConfig(page: Page, config: Record<string, unknown>): void {
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

// Returns the backend method's parsed shape, not the gateway's wire shape.
function stubGatewayModels(page: Page, models: Record<string, unknown>[]): void {
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

// Stateful so a save, re-read, and report are actually checked against each other.
function stubGatewayCredential(
  page: Page,
  host: { hint?: string; endpoint?: string; reusable?: boolean } | null,
  saved: { hint?: string } | null,
): void {
  // `reusable: false` means a key is present, but for another gateway.
  const hostUsable = host !== null && host.reusable !== false;
  let source = saved === null ? (hostUsable ? 'host' : undefined) : 'saved';
  let hint = saved?.hint ?? (hostUsable ? host?.hint : undefined);
  const answer = async (route: Route): Promise<void> =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        data: { ref: 'claude-gateway', source, hint, hostEndpoint: host?.endpoint },
      }),
    });
  void page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string; args?: unknown[] };
    if (body.method === 'LoadGatewayCredentialStatus') {
      await answer(route);
      return;
    }
    if (body.method === 'SaveGatewayCredential') {
      const arg = (body.args ?? [])[0];
      const token = typeof arg === 'string' ? arg : '';
      source = 'saved';
      hint = `…${token.slice(-4)}`;
      await answer(route);
      return;
    }
    if (body.method === 'ClearGatewayCredential') {
      source = hostUsable ? 'host' : undefined;
      hint = hostUsable ? host?.hint : undefined;
      await answer(route);
      return;
    }
    await route.continue();
  });
}

// Called before staging too: the catalog is shared root config, and a leftover
// row with the same model id would read as a persistence bug.
async function clearCatalog(dialog: GlobalConfigDialog): Promise<void> {
  // A saved key outlives the dialog, so it is cleared first.
  if (await dialog.openRouterClearKeyButton().isVisible()) {
    await dialog.openRouterClearKeyButton().click();
  }
  await dialog.setOpenRouterBaseURL('');
  // One row per attempt, so the budget must scale with the catalog rather than
  // expect's 10s default; a tight interval keeps it for the removals.
  await expect
    .poll(
      async () => {
        const remaining = await dialog.openRouterModelRows().count();
        if (remaining > 0) {
          await dialog.openRouterRemoveModelButton(remaining - 1).click();
        }
        return remaining;
      },
      { timeout: withTestBudget().timeout, intervals: [50] },
    )
    .toBe(0);
}

// A leftover gateway would change every later spec's selectable models.
async function restoreCatalog(dialog: GlobalConfigDialog): Promise<void> {
  await clearCatalog(dialog);
  await dialog.save();
  await dialog.waitForClosed();
}

test.describe('erun-level gateway catalog', () => {
  test('renders the gateway fields for an unconfigured install', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    await expect(app.globalConfigDialog.openRouterGatewayTrigger()).toContainText('Not configured');
    await expect(app.globalConfigDialog.openRouterBaseURLInput()).toBeHidden();
    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toHaveCount(0);
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(0);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('reports the key this machine’s Claude Code already uses', async ({ app, page }) => {
    stubGatewayCredential(page, { hint: '…a1b2' }, null);

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');

    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toHaveAttribute(
      'data-gateway-credential-source',
      'host',
    );
    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toContainText('…a1b2');
    await expect(app.globalConfigDialog.openRouterClearKeyButton()).toHaveCount(0);

    await restoreCatalog(app.globalConfigDialog);
  });

  test('says a key on this machine belongs to another gateway, rather than that none exists', async ({
    app,
    page,
  }) => {
    // A key is only sent to the gateway it was issued for.
    stubGatewayCredential(
      page,
      { endpoint: 'https://gateway.example.com/anthropic', reusable: false },
      null,
    );

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');

    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toHaveAttribute(
      'data-gateway-credential-source',
      'none',
    );
    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toContainText(
      'No key for this gateway',
    );
    await expect(app.globalConfigDialog.locator()).toContainText(
      'https://gateway.example.com/anthropic',
    );
    await expect(app.globalConfigDialog.openRouterClearKeyButton()).toHaveCount(0);

    await restoreCatalog(app.globalConfigDialog);
  });

  test('asks for a key when this machine has none, and keeps it out of sight', async ({
    app,
    page,
  }) => {
    stubGatewayCredential(page, null, null);

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');

    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toHaveAttribute(
      'data-gateway-credential-source',
      'none',
    );

    await app.globalConfigDialog.openRouterSetKeyButton().click();
    await expect(app.globalConfigDialog.openRouterTokenInput()).toHaveAttribute('type', 'password');
    await app.globalConfigDialog.openRouterTokenInput().fill('sk-or-v1-secret-wxyz');
    await app.globalConfigDialog.openRouterSaveKeyButton().click();

    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toHaveAttribute(
      'data-gateway-credential-source',
      'saved',
    );
    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toContainText('…wxyz');
    expect(await app.page.content()).not.toContain('sk-or-v1-secret-wxyz');

    await expect(app.globalConfigDialog.openRouterClearKeyButton()).toBeVisible();

    await restoreCatalog(app.globalConfigDialog);
  });

  test('opens pre-filled from this machine’s own gateway', async ({ app, page }) => {
    // An unconfigured catalog pre-fills from this machine's Claude Code settings
    // without storing anything.
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

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('refuses a model id the launch would silently drop', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await clearCatalog(app.globalConfigDialog);

    await app.globalConfigDialog.openRouterAddModelButton().click();
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(1);
    await app.globalConfigDialog.openRouterModelIdInput(0).fill('a b; rm -rf /');
    await expect(
      app.globalConfigDialog
        .locator()
        .getByText('A model id may contain only letters, digits, and . _ : / -'),
    ).toBeVisible();

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
    // The second entry has no display name, so both label shapes are exercised.
    stubGatewayModels(page, [
      { id: 'deepseek/deepseek-v4.1-flash', displayName: 'DeepSeek V4.1 Flash', context: 1048576 },
      { id: 'openai/gpt-6-astra', context: 1050000 },
    ]);

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await clearCatalog(app.globalConfigDialog);
    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');
    await app.globalConfigDialog.openRouterAddModelButton().click();
    await expect(app.globalConfigDialog.openRouterModelIdInput(0)).toBeVisible();

    await app.globalConfigDialog.loadGatewayModels();

    // Searching by display name finds an entry whose id differs.
    await app.globalConfigDialog.openRouterModelChoicesButton(0).click();
    await app.globalConfigDialog.openRouterModelSearchInput().fill('GPT-6');
    await expect(app.page.getByRole('option', { name: 'openai/gpt-6-astra' })).toBeVisible();
    await expect(
      app.page.getByRole('option', { name: 'deepseek/deepseek-v4.1-flash' }),
    ).toBeHidden();
    await app.page.getByRole('option', { name: 'openai/gpt-6-astra' }).click();

    await expect(app.globalConfigDialog.openRouterModelIdInput(0)).toHaveValue(
      'openai/gpt-6-astra',
    );
    await expect(app.globalConfigDialog.openRouterModelContextInput(0)).toHaveValue('1050000');

    await restoreCatalog(app.globalConfigDialog);
  });

  test('saves the catalog and shows it again on reopen', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await clearCatalog(app.globalConfigDialog);

    // Both rows share a model id, so a leaked row would only show as a wrong context.
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(0);

    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');
    await app.globalConfigDialog.addOpenRouterModel({
      id: 'deepseek/deepseek-v4.1-flash',
      context: 1048576,
    });
    // Checked before the save to tell a staging failure from a persistence one.
    await expect(app.globalConfigDialog.openRouterModelIdInput(0)).toHaveValue(
      'deepseek/deepseek-v4.1-flash',
    );
    await expect(app.globalConfigDialog.openRouterModelContextInput(0)).toHaveValue('1048576');
    await app.globalConfigDialog.save();
    await app.globalConfigDialog.waitForClosed();

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await expect(app.globalConfigDialog.openRouterGatewayTrigger()).toContainText(
      'https://openrouter.ai/api',
    );
    await expect(app.globalConfigDialog.openRouterModelIdInput(0)).toHaveValue(
      'deepseek/deepseek-v4.1-flash',
    );
    await expect(app.globalConfigDialog.openRouterModelContextInput(0)).toHaveValue('1048576');

    await restoreCatalog(app.globalConfigDialog);
  });

  test('keeps a declared reasoning-echo model out of the default choices', async ({ app }) => {
    // A reasoning-echo listing must persist and never be offered as the default
    // model, since no erun AI lane can supply the reasoning back.
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await clearCatalog(app.globalConfigDialog);
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(0);

    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');
    await app.globalConfigDialog.addOpenRouterModel({
      id: 'deepseek/deepseek-v4.1-flash',
      context: 1048576,
    });
    await expect(app.globalConfigDialog.openRouterReasoningEchoCheckbox(0)).not.toBeChecked();
    await app.globalConfigDialog.openRouterReasoningEchoCheckbox(0).check();
    await expect(app.globalConfigDialog.openRouterReasoningEchoCheckbox(0)).toBeChecked();

    await app.globalConfigDialog.openRouterDefaultModelTrigger().click();
    await expect(
      app.page.getByRole('option', { name: 'deepseek/deepseek-v4.1-flash' }),
    ).toBeHidden();
    await app.page.keyboard.press('Escape');

    await app.globalConfigDialog.save();
    await app.globalConfigDialog.waitForClosed();

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    // The editor writes the whole catalog back, so a dropped field would be lost here.
    await expect(app.globalConfigDialog.openRouterReasoningEchoCheckbox(0)).toBeChecked();

    await restoreCatalog(app.globalConfigDialog);
  });

  // A failed clear skips restoreCatalog's save and leaks this gateway into the
  // next spec, so the reopen checks the restore landed in shared config.
  test('a catalog larger than a fixed step cap is cleared, not abandoned half-way', async ({
    app,
  }) => {
    test.setTimeout(60_000);
    const CATALOG_SIZE = 16;
    const dialog = app.globalConfigDialog;

    await app.sidebar.openSettings();
    await dialog.waitForOpen();
    await clearCatalog(dialog);
    await dialog.setOpenRouterBaseURL('https://openrouter.ai/api');
    for (let i = 0; i < CATALOG_SIZE; i++) {
      await dialog.openRouterAddModelButton().click();
      await expect(dialog.openRouterModelRows()).toHaveCount(i + 1);
      await dialog.openRouterModelIdInput(i).fill('deepseek/deepseek-v4.1-flash');
    }

    await restoreCatalog(dialog);

    await app.sidebar.openSettings();
    await dialog.waitForOpen();
    await expect(dialog.openRouterGatewayTrigger()).toContainText('Not configured');
    await expect(dialog.openRouterModelRows()).toHaveCount(0);

    await dialog.cancel();
    await dialog.waitForClosed();
  });
});
