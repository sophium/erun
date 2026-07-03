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

  // --- Add-provider picker ---
  //
  // Adding a provider delegates to the CLI's guided `erun cloud init` flow, not
  // an in-app form; the add buttons launch that flow and close the dialog.

  addAWSButton(): Locator {
    return this.locator().getByRole('button', { name: 'AWS', exact: true });
  }

  addCloudflareButton(): Locator {
    return this.locator().getByRole('button', { name: 'Cloudflare', exact: true });
  }

  async clickAddAWS(): Promise<void> {
    await this.addAWSButton().click();
  }

  async clickAddCloudflare(): Promise<void> {
    await this.addCloudflareButton().click();
  }

  // The in-app add form was removed in favour of the guided CLI flow; specs use
  // this locator to assert it resolves to zero matches — the negative invariant
  // that the desktop hosts no bespoke add form.
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
