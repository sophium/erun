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

// stubCredentialCandidates answers the Secret read, so the picker can be
// exercised without a cluster. It stands in for the backend method, so it
// returns that method's own result shape.
function stubCredentialCandidates(
  page: import('@playwright/test').Page,
  result: Record<string, unknown>,
): void {
  void page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== 'LoadGatewayCredentialCandidates') {
      await route.continue();
      return;
    }
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: result }),
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
    // An unset credential reads as the default the gateway will actually use,
    // rather than a blank field that silently means one. Nothing is stored by
    // rendering it; the operator still saves.
    await expect(app.globalConfigDialog.openRouterSecretInput()).toHaveValue('erun-claude-gateway');
    await expect(app.globalConfigDialog.openRouterSecretKeyInput()).toHaveValue('token');
    // Both fields carry a usable value, so neither is labelled optional.
    await expect(app.page.getByText('Credential Secret (optional)')).toHaveCount(0);
    await expect(app.page.getByText('Secret key (optional)')).toHaveCount(0);
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

    // The choice is searched rather than scrolled, which is the whole point:
    // a gateway can serve hundreds of models. Searching by the display name
    // finds the entry that launches with a different id.
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

  test('picks a credential Secret that already exists', async ({ app, page }) => {
    // The Secret lives per environment namespace, so the names that exist are
    // read and offered rather than invented. A namespace that could not be read
    // is named: a Secret whose access is denied must not look like one that is
    // missing.
    stubCredentialCandidates(page, {
      candidates: [
        { name: 'erun-claude-gateway', namespaces: ['pw-alpha'], keys: ['token'] },
        {
          name: 'tenant-claude-gateway',
          namespaces: ['pw-alpha', 'pw-beta'],
          keys: ['token', 'refresh'],
        },
      ],
      problems: ['pw-beta: secrets is forbidden'],
      namespaces: 2,
    });

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    await app.globalConfigDialog.openRouterFindSecretsButton().click();
    await app.globalConfigDialog.selectOpenRouterSecret('tenant-claude-gateway');
    await expect(app.globalConfigDialog.openRouterSecretInput()).toHaveValue(
      'tenant-claude-gateway',
    );

    // The unreadable namespace is surfaced beside the choices.
    await expect(
      app.globalConfigDialog.locator().getByText('pw-beta: secrets is forbidden'),
    ).toBeVisible();

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
