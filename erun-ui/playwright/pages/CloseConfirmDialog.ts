import type { Locator, Page } from '@playwright/test';

// Shown when the operator tries to close the window while a build/deploy/
// release is still running (erun#1214). Its title is unique across dialogs,
// so the anchored role match stays unambiguous.
export class CloseConfirmDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'Close ERun while work is still running?' });
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  async isOpen(): Promise<boolean> {
    return this.locator()
      .isVisible()
      .catch(() => false);
  }

  async cancel(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Cancel' }).click();
  }

  async closeAnyway(): Promise<void> {
    await this.locator().getByRole('button', { name: 'Close anyway' }).click();
  }
}
