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
  // type. data-cloud-alias-group carries the provider type token.
  cloudAliasGroupHeading(providerType: string): Locator {
    return this.locator().locator(`[data-cloud-alias-group="${providerType}"]`);
  }

  // --- Add-provider picker ---
  //
  // Both providers delegate alias creation to the CLI's guided `erun cloud init
  // <provider>` flow over a PTY session; neither hosts an in-app add form. The
  // add buttons launch the session and close the settings dialog, handing the
  // terminal over to the CLI.

  // addAWSButton targets the "AWS" add button in the provider picker.
  addAWSButton(): Locator {
    return this.locator().getByRole('button', { name: 'AWS', exact: true });
  }

  // addCloudflareButton targets the "Cloudflare" add button in the provider
  // picker.
  addCloudflareButton(): Locator {
    return this.locator().getByRole('button', { name: 'Cloudflare', exact: true });
  }

  async clickAddAWS(): Promise<void> {
    await this.addAWSButton().click();
  }

  async clickAddCloudflare(): Promise<void> {
    await this.addCloudflareButton().click();
  }

  // cloudflareForm targets the removed in-app "add Cloudflare token" form
  // (deleted in favour of the guided CLI flow). Specs assert this
  // resolves to zero matches — the negative invariant that no bespoke add form
  // is hosted in the desktop.
  cloudflareForm(): Locator {
    return this.page.locator('form[aria-label="Add Cloudflare token"]');
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
