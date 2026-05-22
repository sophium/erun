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
    return this.locator().locator(`text=${alias}`).first();
  }

  async refreshCloudProviders(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh cloud aliases' }).click();
  }

  async refreshCloudContexts(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh cloud contexts' }).click();
  }

  async cancel(): Promise<void> {
    const button = this.locator().getByRole('button', { name: 'Cancel' });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }

  async save(): Promise<void> {
    const button = this.locator().getByRole('button', { name: /Save settings|Saving/ });
    await button.scrollIntoViewIfNeeded();
    await button.click();
  }
}
