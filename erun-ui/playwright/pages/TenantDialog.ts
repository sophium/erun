import type { Locator, Page } from '@playwright/test';

// TenantDialog POM. The dialog title is "Manage tenant" with the tenant
// name in the description.
export class TenantDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'Manage tenant' });
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  defaultEnvironmentTrigger(): Locator {
    return this.page.locator('#tenant-config-defaultenvironment');
  }

  apiUrlInput(): Locator {
    return this.page.locator('#tenant-config-apiurl');
  }

  cloudAliasCheckbox(alias: string): Locator {
    return this.page.getByRole('checkbox', { name: `Trust ${alias}` });
  }

  async save(): Promise<void> {
    await this.locator().getByRole('button', { name: /^Sav/ }).click();
  }

  async cancel(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Cancel' }).click();
  }
}
