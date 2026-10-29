import { expect, type Locator, type Page } from '@playwright/test';

export interface DialogFrameBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

// A member is null only when its element is absent; specs assert on that.
export interface DialogFrameGeometry {
  dialog: DialogFrameBox | null;
  title: DialogFrameBox | null;
  cancel: DialogFrameBox | null;
  save: DialogFrameBox | null;
  body: { scrollHeight: number; clientHeight: number } | null;
}

export class GlobalConfigDialog {
  constructor(public readonly page: Page) {}

  // includeHidden: Radix's modal Select aria-hides the dialog through its close transition,
  // and a role query would then count zero rows.
  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'ERun settings', includeHidden: true });
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  // waitForLoadedFrame waits for the loaded body (not the loading shell) and the enter animation.
  async waitForLoadedFrame(): Promise<void> {
    await this.locator().locator('#global-config-defaulttenant').waitFor({ state: 'visible' });
    await this.locator().evaluate(async (root) => {
      await Promise.all(root.getAnimations().map((a) => a.finished.catch(() => undefined)));
    });
  }

  // frameGeometry reads everything in one evaluation, so no layout change lands between reads.
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
    // The trigger's text is the selected value, so match by id.
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
  // AWS and Cloudflare launch `erun cloud init` and close the dialog; erun uses a popover.

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

  // Specs assert this resolves to nothing: there is no in-app Cloudflare form.
  cloudflareForm(): Locator {
    return this.page.locator('form[aria-label="Add Cloudflare token"]');
  }

  // The popover is portaled to document.body.
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

  openRouterBaseURLInput(): Locator {
    return this.page.locator('#global-config-openrouter-baseurl');
  }

  // The credential is reported by source, not typed into a field.
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

  // Searched, not scrolled; the option name includes the id.
  async selectOpenRouterModel(index: number, id: string, search = id): Promise<void> {
    await this.openRouterModelChoicesButton(index).click();
    await this.openRouterModelSearchInput().fill(search);
    await this.page.getByRole('option', { name: id }).click();
  }

  // addOpenRouterModel takes the index from the pre-click count and waits for it to rise,
  // then asserts both fields so a lost fill fails here, not after save.
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
