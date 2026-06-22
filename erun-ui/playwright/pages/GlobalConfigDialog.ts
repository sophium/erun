import type { Locator, Page } from '@playwright/test';

// GlobalConfigDialog POM. The dialog is reached from the sidebar gear icon
// (Sidebar.openSettings) and exposes the default-tenant selector plus the
// cloud-alias and cloud-context lists.
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
    // SelectField wraps Radix Select, whose trigger is a button with the
    // selected value as its accessible text. Match it by id-based selector
    // since the visible label maps to id="global-config-defaulttenant".
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

  // cloudAliasGroupHeading targets the labelled group heading for a provider
  // type (issue #630). data-cloud-alias-group carries the provider type token.
  cloudAliasGroupHeading(providerType: string): Locator {
    return this.locator().locator(`[data-cloud-alias-group="${providerType}"]`);
  }

  // --- Add-provider picker + Cloudflare form (issue #630) ---

  // addAWSButton targets the "AWS" add button in the provider picker (opens an
  // SSO PTY session).
  addAWSButton(): Locator {
    return this.locator().getByRole('button', { name: 'AWS', exact: true });
  }

  // addCloudflareButton targets the "Cloudflare" add button in the provider
  // picker (reveals the inline masked-token form).
  addCloudflareButton(): Locator {
    return this.locator().getByRole('button', { name: 'Cloudflare', exact: true });
  }

  async openCloudflareForm(): Promise<void> {
    await this.addCloudflareButton().click();
  }

  cloudflareForm(): Locator {
    return this.locator().locator('form[aria-label="Add Cloudflare token"]');
  }

  cloudflareAccountIdInput(): Locator {
    return this.locator().locator('#global-config-cloudflare-accountid');
  }

  cloudflareTokenNameInput(): Locator {
    return this.locator().locator('#global-config-cloudflare-tokenname');
  }

  cloudflareApiTokenInput(): Locator {
    return this.locator().locator('#global-config-cloudflare-apitoken');
  }

  cloudflareSubmitButton(): Locator {
    return this.cloudflareForm().getByRole('button', { name: /Add token|Verifying/ });
  }

  async fillCloudflareForm(input: {
    accountId: string;
    tokenName: string;
    apiToken: string;
  }): Promise<void> {
    await this.cloudflareAccountIdInput().fill(input.accountId);
    await this.cloudflareTokenNameInput().fill(input.tokenName);
    await this.cloudflareApiTokenInput().fill(input.apiToken);
  }

  async refreshCloudProviders(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh cloud aliases' }).click();
  }

  async refreshCloudContexts(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh cloud contexts' }).click();
  }

  async cancel(): Promise<void> {
    // Match the footer Cancel exactly: the Cloudflare add form's close button
    // is labelled "Cancel adding Cloudflare token", which a substring match
    // would also resolve.
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
