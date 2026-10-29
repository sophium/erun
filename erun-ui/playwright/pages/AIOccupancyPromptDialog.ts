import type { Locator, Page } from '@playwright/test';

// Shown when the AI tab finds the environment leased by another job.
export class AIOccupancyPromptDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'Another agent is already working here' });
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  async cancel(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Cancel' }).click();
  }

  async startAnyway(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Start anyway' }).click();
  }
}
