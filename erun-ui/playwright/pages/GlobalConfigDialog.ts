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
