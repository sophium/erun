import type { Page, Request, Route } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';
import type { GlobalConfigDialog } from '../../../pages/GlobalConfigDialog.js';

// stubERunConfig answers the config read, so a spec can stage the machine's own
// gateway defaults without touching the developer's real user settings.
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

// stubGatewayModels answers the gateway model-list read with a fixed catalog, so
// the picker can be exercised without a live gateway. It stands in for the
// backend method, so it returns that method's own result shape — the parsed
// {id, displayName, context} — not the gateway's wire shape, which the Go side
// parses and covers in its own tests.
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

// stubGatewayCredential answers the credential reads and writes with a small
// stateful fake of the erun-level store.
//
// Stateful because the panel saves through the same backend methods this stub
// answers, and the assertion after a save is about the flow: a stub that kept
// returning its first answer would be asserting on itself rather than on
// whether saving, re-reading, and reporting actually agree.
function stubGatewayCredential(
  page: Page,
  host: { hint?: string; endpoint?: string; reusable?: boolean } | null,
  saved: { hint?: string } | null,
): void {
  // reusable models the backend's own decision: a key is only reported as this
  // gateway's when the host's settings already point at it. `reusable: false` is
  // the mismatch — a key is present, for somewhere else.
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

// clearCatalog empties the catalog editor with the settings dialog already open.
//
// Specs call it BEFORE staging their own rows, not only after. The catalog is
// shared root config, so a row an earlier spec left behind is otherwise read as
// this spec's own — and because every spec here uses the same model id, that
// shows up as a wrong value rather than as a stray row, which reads like a
// persistence bug. Staging from a known-empty catalog makes each spec
// independent of whatever ran before it in its worker.
async function clearCatalog(dialog: GlobalConfigDialog): Promise<void> {
  // A saved key outlives the dialog, so it goes first: a later spec's panel would
  // otherwise report a key this one saved.
  if (await dialog.openRouterClearKeyButton().isVisible()) {
    await dialog.openRouterClearKeyButton().click();
  }
  await dialog.setOpenRouterBaseURL('');
  // Remove from the end until none remain. This is driven by a poll rather than a
  // count read once: the loop's exit condition IS "no rows left", so it cannot
  // stop on a count that raced the render and leave one behind.
  await expect
    .poll(async () => {
      const remaining = await dialog.openRouterModelRows().count();
      if (remaining > 0) {
        await dialog.openRouterRemoveModelButton(remaining - 1).click();
      }
      return remaining;
    })
    .toBe(0);
}

// restoreCatalog leaves the suite's global config as it found it. These specs
// mutate shared root config rather than an environment, and the worker's backend
// is shared with every other spec, so leaving a gateway configured would swap
// every later spec's selectable models for the catalog's.
async function restoreCatalog(dialog: GlobalConfigDialog): Promise<void> {
  await clearCatalog(dialog);
  await dialog.save();
  await dialog.waitForClosed();
}

// The erun-level gateway catalog is edited in ERun settings and consumed by
// every environment's AI tab, so the observable contract is: the fields render,
// the credential is reported rather than typed at, a model id the launch would
// drop is refused at entry, and what is saved comes back on reopen.
test.describe('erun-level gateway catalog', () => {
  test('renders the gateway fields for an unconfigured install', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();

    // The gateway is chosen, so the URL field only appears for a self-hosted
    // address; an unconfigured install shows the selector at Not configured.
    await expect(app.globalConfigDialog.openRouterGatewayTrigger()).toContainText('Not configured');
    await expect(app.globalConfigDialog.openRouterBaseURLInput()).toBeHidden();
    // A key for a gateway that does not exist is a field that changes nothing,
    // so neither the credential panel nor any rows are offered yet.
    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toHaveCount(0);
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(0);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('reports the key this machine’s Claude Code already uses', async ({ app, page }) => {
    // The credential is one erun-level value, and this machine already has one:
    // an operator who runs Claude Code against a gateway should be told their key
    // will be used, not asked to paste it in again. Only a suffix is ever shown.
    stubGatewayCredential(page, { hint: '…a1b2' }, null);

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');

    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toHaveAttribute(
      'data-gateway-credential-source',
      'host',
    );
    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toContainText('…a1b2');
    // Nothing is saved, so offering to switch back to the machine's key would
    // offer the state it is already in.
    await expect(app.globalConfigDialog.openRouterClearKeyButton()).toHaveCount(0);

    await restoreCatalog(app.globalConfigDialog);
  });

  test('says a key on this machine belongs to another gateway, rather than that none exists', async ({
    app,
    page,
  }) => {
    // The operator can see their own Claude Code key, so a panel reading "no key
    // found" would contradict it. What they need to know is that the key is not
    // sent here — a credential is only ever sent to the gateway it was issued
    // for — and which endpoint it does belong to.
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
    // The other endpoint is named, so the operator can tell which key is which.
    await expect(app.globalConfigDialog.locator()).toContainText(
      'https://gateway.example.com/anthropic',
    );
    // Nothing is saved, so there is no "switch back" to offer.
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
    // A password field: a gateway key typed into a dialog should not be readable
    // over a shoulder or in a screenshot.
    await expect(app.globalConfigDialog.openRouterTokenInput()).toHaveAttribute('type', 'password');
    await app.globalConfigDialog.openRouterTokenInput().fill('sk-or-v1-secret-wxyz');
    await app.globalConfigDialog.openRouterSaveKeyButton().click();

    // Saving re-reads the status rather than assuming: the panel reports what the
    // backend now holds, and shows only its suffix.
    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toHaveAttribute(
      'data-gateway-credential-source',
      'saved',
    );
    await expect(app.globalConfigDialog.openRouterCredentialSummary()).toContainText('…wxyz');
    // The whole token is nowhere in the document, including the input it was
    // typed into: the panel is closed by then, and a value left rendered would
    // be readable in a screenshot.
    expect(await app.page.content()).not.toContain('sk-or-v1-secret-wxyz');

    // Only now is switching back to the machine's key a change worth offering.
    await expect(app.globalConfigDialog.openRouterClearKeyButton()).toBeVisible();

    await restoreCatalog(app.globalConfigDialog);
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
    await clearCatalog(app.globalConfigDialog);

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
    await clearCatalog(app.globalConfigDialog);
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

  test('saves the catalog and shows it again on reopen', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await clearCatalog(app.globalConfigDialog);

    // Asserted after clearing, not assumed: this spec reads row 0 on reopen, so
    // a row that survived the clear would be the one read. Both rows carry the
    // same model id, so the id assertion would still pass and only the context
    // would look wrong — exactly the confusing shape a leak produces.
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(0);

    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');
    await app.globalConfigDialog.addOpenRouterModel({
      id: 'deepseek/deepseek-v4.1-flash',
      context: 1048576,
    });
    // The row must hold both figures before the save: asserting only after the
    // reopen cannot tell a staging failure from a persistence one, and a save
    // that silently dropped the window would look like a persistence bug.
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
    // Declaring the listing is how an operator records that the provider
    // demands the model's own reasoning back, which no erun AI lane can supply.
    // Two things must survive: the declaration itself, and the listing then not
    // being offered as the default an environment renders as ANTHROPIC_MODEL
    // and an exec agent job starts on.
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await clearCatalog(app.globalConfigDialog);
    await expect(app.globalConfigDialog.openRouterModelRows()).toHaveCount(0);

    await app.globalConfigDialog.setOpenRouterBaseURL('https://openrouter.ai/api');
    await app.globalConfigDialog.addOpenRouterModel({
      id: 'deepseek/deepseek-v4.1-flash',
      context: 1048576,
    });
    // Undeclared by default, so the control is reporting state rather than
    // always reading as on.
    await expect(app.globalConfigDialog.openRouterReasoningEchoCheckbox(0)).not.toBeChecked();
    await app.globalConfigDialog.openRouterReasoningEchoCheckbox(0).check();
    await expect(app.globalConfigDialog.openRouterReasoningEchoCheckbox(0)).toBeChecked();

    // The default list is the model an environment starts on, so a declared
    // listing must not appear in it however the row is otherwise filled in.
    await app.globalConfigDialog.openRouterDefaultModelTrigger().click();
    await expect(
      app.page.getByRole('option', { name: 'deepseek/deepseek-v4.1-flash' }),
    ).toBeHidden();
    await app.page.keyboard.press('Escape');

    await app.globalConfigDialog.save();
    await app.globalConfigDialog.waitForClosed();

    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    // The declaration survives the save. The editor writes the whole catalog
    // back, so a conversion that dropped this field would delete it here and
    // quietly return the row to the selectable set on the operator's first edit.
    await expect(app.globalConfigDialog.openRouterReasoningEchoCheckbox(0)).toBeChecked();

    await restoreCatalog(app.globalConfigDialog);
  });
});
