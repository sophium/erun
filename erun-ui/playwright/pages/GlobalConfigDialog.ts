import { expect, type Locator, type Page } from '@playwright/test';

export interface DialogFrameBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

// The dialog frame and its contents as one layout snapshot; a member is null
// only when its element is absent from that snapshot, which a spec asserts on
// rather than silently skipping (see boundingBoxOf's note on the same rule).
export interface DialogFrameGeometry {
  dialog: DialogFrameBox | null;
  title: DialogFrameBox | null;
  cancel: DialogFrameBox | null;
  save: DialogFrameBox | null;
  body: { scrollHeight: number; clientHeight: number } | null;
}

export class GlobalConfigDialog {
  constructor(public readonly page: Page) {}

  // The dialog is matched even while it is hidden from the accessibility tree.
  //
  // Radix's modal Select hides the rest of the document with aria-hidden for as
  // long as its content is mounted, and that content stays mounted through its
  // close transition — so for a moment after every gateway-select interaction
  // every element in this dialog, including the dialog itself, is aria-hidden
  // while sitting plainly in the DOM. A role query skips those elements, so
  // anything derived from this locator resolves to a clean, wrong zero: the
  // catalog's clear loop reads "no rows to remove" and exits without removing
  // any, a `toHaveCount(0)` guard passes vacuously, and the pre-click index is
  // taken as 0 against a catalog that already holds a row — one Add then makes
  // two. Every one of those reads follows a select interaction, which is why the
  // window lands on all of them. Matching the element rather than what a screen
  // reader would currently see keeps a count a count.
  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'ERun settings', includeHidden: true });
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  // Converge on the frame this dialog settles into once its config has landed.
  //
  // `waitForOpen` resolves the moment the content mounts, which is the
  // `configLoading` shell: the header and the footer are already there, the body
  // is one placeholder line, and the whole card is ~195px tall inside a viewport
  // whose loaded frame is capped at 85vh (~1020px). Geometry read before the
  // config lands is therefore geometry of a different card than the one the
  // assertions are about. The body's own first field is the observable signal
  // that the load landed -- `GlobalConfigBody` renders the placeholder instead
  // of it for as long as `configLoading` is set -- and the enter animation
  // (DialogContent's `zoom-in-95`, 200ms) is waited out so a measurement is not
  // taken part-way through a scale transform.
  async waitForLoadedFrame(): Promise<void> {
    await this.locator().locator('#global-config-defaulttenant').waitFor({ state: 'visible' });
    await this.locator().evaluate(async (root) => {
      await Promise.all(root.getAnimations().map((a) => a.finished.catch(() => undefined)));
    });
  }

  // The dialog frame and everything asserted inside it, read from ONE layout.
  //
  // As separate locator round trips these are separate layout moments, and this
  // card's height changes by ~815px between its loading shell and its loaded
  // body: a frame read before the config lands and a footer read after it are
  // ~382px apart, so a containment assertion across the two fails on the loading
  // shell's own bottom while the footer is legitimately inside the loaded one.
  // This dialog is the one that measured it -- see the spec's own note. One
  // evaluation, one layout: no transition can land between two reads that are
  // not two reads.
  async frameGeometry(): Promise<DialogFrameGeometry> {
    return this.locator().evaluate((root) => {
      const box = (el: Element | null): DialogFrameBox | null => {
        if (!(el instanceof HTMLElement)) return null;
        const rect = el.getBoundingClientRect();
        return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
      };
      const label = (el: Element): string => (el.textContent ?? '').trim();
      const footer = root.querySelector('[data-slot="dialog-footer"]');
      const buttons = footer ? Array.from(footer.querySelectorAll('button')) : [];
      const body = root.querySelector('.overflow-y-auto');
      return {
        dialog: box(root),
        title: box(root.querySelector('[data-slot="dialog-title"]')),
        cancel: box(buttons.find((b) => label(b) === 'Cancel') ?? null),
        save: box(buttons.find((b) => /^(Save settings|Saving\.\.\.)$/.test(label(b))) ?? null),
        body: body ? { scrollHeight: body.scrollHeight, clientHeight: body.clientHeight } : null,
      };
    });
  }

  defaultTenantTrigger(): Locator {
    // The trigger's accessible text is the selected value, not a stable label,
    // so match it by id rather than by role name.
    return this.page.locator('#global-config-defaulttenant');
  }

  async getDefaultTenant(): Promise<string> {
    return (await this.defaultTenantTrigger().textContent()) || '';
  }

  async selectDefaultTenant(name: string): Promise<void> {
    await this.defaultTenantTrigger().click();
    await this.page.getByRole('option', { name }).click();
  }

  cloudContextProviderTrigger(): Locator {
    return this.page.locator('#global-config-cloudcontext-provider');
  }

  cloudContextRegionTrigger(): Locator {
    return this.page.locator('#global-config-cloudcontext-region');
  }

  cloudContextProviderValue(): Locator {
    return this.cloudContextProviderTrigger().locator('[data-slot="select-value"]');
  }

  cloudAliasRow(alias: string): Locator {
    return this.locator().locator(`[data-cloud-alias="${alias}"]`).first();
  }

  cloudAliasGroupHeading(providerType: string): Locator {
    return this.locator().locator(`[data-cloud-alias-group="${providerType}"]`);
  }

  cloudAliasLogoutButton(alias: string): Locator {
    return this.cloudAliasRow(alias).getByRole('button', { name: /Log out|Logging out/ });
  }

  cloudAliasSwitchIdentityButton(alias: string): Locator {
    return this.cloudAliasRow(alias).getByRole('button', {
      name: /Sign in as someone else|Switching/,
    });
  }

  async logoutCloudAlias(alias: string): Promise<void> {
    await this.cloudAliasLogoutButton(alias).click();
  }

  async switchCloudAliasIdentity(alias: string): Promise<void> {
    await this.cloudAliasSwitchIdentityButton(alias).click();
  }

  // --- Add-provider actions ---
  //
  // AWS and Cloudflare delegate to the CLI's guided `erun cloud init` flow, not
  // an in-app form; those add buttons launch that flow and close the dialog.
  // erun collects its one required field (the platform API URL) in a popover
  // without leaving Settings — see addERunButton/erunApiUrlInput/connectERun.
  // The same three buttons render in the header (once aliases exist) and in
  // the empty state (before any do) — never both at once.

  addAWSButton(): Locator {
    return this.locator().getByRole('button', { name: 'Add AWS account', exact: true });
  }

  addCloudflareButton(): Locator {
    return this.locator().getByRole('button', { name: 'Add Cloudflare token', exact: true });
  }

  addERunButton(): Locator {
    return this.locator().getByRole('button', { name: 'Add erun platform', exact: true });
  }

  async clickAddAWS(): Promise<void> {
    await this.addAWSButton().click();
  }

  async clickAddCloudflare(): Promise<void> {
    await this.addCloudflareButton().click();
  }

  // The in-app add form was removed in favour of the guided CLI flow; specs use
  // this locator to assert it resolves to zero matches — the negative invariant
  // that the desktop hosts no bespoke add form for Cloudflare.
  cloudflareForm(): Locator {
    return this.page.locator('form[aria-label="Add Cloudflare token"]');
  }

  // The erun add popover is portal'd to document.body by Radix, so these
  // query at page level rather than inside the dialog locator.
  erunApiUrlInput(): Locator {
    return this.page.locator('#cloud-alias-erun-api-url');
  }

  erunConnectButton(): Locator {
    return this.page.locator('[data-slot="popover-content"]').getByRole('button', {
      name: /Connect|Connecting/,
    });
  }

  async connectERunPlatform(apiUrl: string): Promise<void> {
    await this.addERunButton().click();
    await this.erunApiUrlInput().fill(apiUrl);
    await this.erunConnectButton().click();
  }

  async refreshCloudProviders(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh cloud aliases' }).click();
  }

  async refreshCloudContexts(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh cloud contexts' }).click();
  }

  // --- Gateway catalog ---
  //
  // The erun-level OpenRouter catalog: one list every environment selects from.
  // Rows carry a data-openrouter-model index so a spec addresses the row it
  // added rather than whichever one happens to be first.

  openRouterBaseURLInput(): Locator {
    return this.page.locator('#global-config-openrouter-baseurl');
  }

  // The gateway credential is reported, not typed into a field: erun delivers
  // one erun-level value, so what an operator picks from is which key is in play.
  // The summary carries its source so a spec asserts on the state rather than on
  // a sentence that may be reworded.
  openRouterCredentialSummary(): Locator {
    return this.locator().locator('[data-gateway-credential-source]');
  }

  openRouterSetKeyButton(): Locator {
    return this.locator().getByRole('button', { name: /Set a key|Use a different key/ });
  }

  openRouterClearKeyButton(): Locator {
    return this.locator().getByRole('button', { name: "Use this machine's key" });
  }

  openRouterTokenInput(): Locator {
    return this.page.locator('#global-config-openrouter-token');
  }

  openRouterSaveKeyButton(): Locator {
    return this.locator().getByRole('button', { name: 'Save key', exact: true });
  }

  async setOpenRouterCredential(token: string): Promise<void> {
    await this.openRouterSetKeyButton().click();
    await this.openRouterTokenInput().fill(token);
    await this.openRouterSaveKeyButton().click();
  }

  openRouterDefaultModelTrigger(): Locator {
    return this.page.locator('#global-config-openrouter-default-model');
  }

  openRouterAddModelButton(): Locator {
    return this.locator().getByRole('button', { name: 'Add model', exact: true });
  }

  openRouterModelRows(): Locator {
    return this.locator().locator('[data-openrouter-model]');
  }

  openRouterModelRow(index: number): Locator {
    return this.locator().locator(`[data-openrouter-model="${String(index)}"]`);
  }

  openRouterModelIdInput(index: number): Locator {
    return this.openRouterModelRow(index).getByLabel(`Model id ${String(index + 1)}`);
  }

  openRouterModelContextInput(index: number): Locator {
    return this.openRouterModelRow(index).getByLabel(
      `Context window for model ${String(index + 1)}`,
    );
  }

  openRouterReasoningEchoCheckbox(index: number): Locator {
    return this.openRouterModelRow(index).getByLabel(
      `Requires reasoning echo for model ${String(index + 1)}`,
    );
  }

  openRouterRemoveModelButton(index: number): Locator {
    return this.openRouterModelRow(index).getByRole('button', {
      name: `Remove model ${String(index + 1)}`,
    });
  }

  openRouterGatewayTrigger(): Locator {
    return this.page.locator('#global-config-openrouter-gateway');
  }

  // A known gateway is chosen from the list; anything else is a self-hosted
  // address, so the field is revealed before it is typed into.
  async setOpenRouterBaseURL(value: string): Promise<void> {
    await this.openRouterGatewayTrigger().click();
    if (value === '') {
      await this.page.getByRole('option', { name: 'Not configured' }).click();
      return;
    }
    if (value === 'https://openrouter.ai/api') {
      await this.page.getByRole('option', { name: /OpenRouter/ }).click();
      return;
    }
    await this.page.getByRole('option', { name: 'Self-hosted (enter a URL)' }).click();
    await this.openRouterBaseURLInput().fill(value);
  }

  openRouterLoadModelsButton(): Locator {
    return this.locator().getByRole('button', { name: /Load from gateway|Reload from gateway/ });
  }

  openRouterModelChoicesButton(index: number): Locator {
    return this.locator().getByRole('button', {
      name: `Show gateway models for model ${String(index + 1)}`,
    });
  }

  openRouterModelSearchInput(): Locator {
    return this.page.getByPlaceholder('Search models...');
  }

  async loadGatewayModels(): Promise<void> {
    await this.openRouterLoadModelsButton().click();
  }

  // The choice is searched, not scrolled: a gateway can serve hundreds of
  // models. The option's accessible name carries the display name and the id,
  // so the id identifies it without depending on a name a gateway may omit.
  async selectOpenRouterModel(index: number, id: string, search = id): Promise<void> {
    await this.openRouterModelChoicesButton(index).click();
    await this.openRouterModelSearchInput().fill(search);
    await this.page.getByRole('option', { name: id }).click();
  }

  // addOpenRouterModel appends a row and fills it.
  //
  // The index comes from the count taken BEFORE the click, and the click is then
  // waited on by expecting that count to rise. Reading the count after the click
  // instead would be a non-retrying query: it can return the pre-click length
  // while the new row is still mounting, which points the fills at the wrong row
  // (or at none) and shows up later as the wrong value on reopen. Deriving the
  // index from a count that is then waited on cannot skew.
  //
  // Both fields are asserted before returning, so a fill that did not stick
  // fails here — where the row is visible — rather than after a save, where it
  // would be indistinguishable from a persistence bug.
  async addOpenRouterModel({ id, context }: { id: string; context?: number }): Promise<void> {
    const rows = this.openRouterModelRows();
    const index = await rows.count();
    await this.openRouterAddModelButton().click();
    await expect(rows).toHaveCount(index + 1);
    await this.openRouterModelIdInput(index).fill(id);
    await expect(this.openRouterModelIdInput(index)).toHaveValue(id);
    if (context !== undefined) {
      await this.openRouterModelContextInput(index).fill(String(context));
      await expect(this.openRouterModelContextInput(index)).toHaveValue(String(context));
    }
  }

  async cancel(): Promise<void> {
    const button = this.locator().getByRole('button', { name: 'Cancel', exact: true });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async save(): Promise<void> {
    const button = this.locator().getByRole('button', { name: /Save settings|Saving/ });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }
}
