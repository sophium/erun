import type { Locator, Page } from '@playwright/test';

export class GlobalConfigDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'ERun settings' });
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
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

  openRouterSecretInput(): Locator {
    return this.page.locator('#global-config-openrouter-secret');
  }

  openRouterSecretKeyInput(): Locator {
    return this.page.locator('#global-config-openrouter-secret-key');
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

  openRouterFindSecretsButton(): Locator {
    return this.locator().getByRole('button', { name: /Find Secrets|Reload Secrets/ });
  }

  openRouterSecretChoicesButton(): Locator {
    return this.locator().getByRole('button', { name: 'Show Credential Secret' });
  }

  // The Secret name is picked from what the environment namespaces hold, so it
  // does not have to be invented.
  async selectOpenRouterSecret(name: string): Promise<void> {
    await this.openRouterSecretChoicesButton().click();
    await this.page.getByRole('option', { name }).click();
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

  async setOpenRouterCredential({ secret, key }: { secret: string; key?: string }): Promise<void> {
    await this.openRouterSecretInput().fill(secret);
    if (key !== undefined) {
      await this.openRouterSecretKeyInput().fill(key);
    }
  }

  async addOpenRouterModel({ id, context }: { id: string; context?: number }): Promise<void> {
    await this.openRouterAddModelButton().click();
    const index = (await this.openRouterModelRows().count()) - 1;
    await this.openRouterModelIdInput(index).fill(id);
    if (context !== undefined) {
      await this.openRouterModelContextInput(index).fill(String(context));
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
