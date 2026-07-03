import type { Locator, Page } from '@playwright/test';

// The Auto-start prompt appears only for a remote env with a stopped cloud
// context and no recorded auto-start preference yet. Its title prefix is
// unique across dialogs, so the anchored role match stays unambiguous.
export class AutoStartPromptDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: /^Auto-start / });
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

  async chooseNever(): Promise<void> {
    await this.locator()
      .getByRole('button', { name: /^Don't auto-start/ })
      .click();
  }

  async chooseAlways(): Promise<void> {
    await this.locator()
      .getByRole('button', { name: /^Auto-start$/ })
      .click();
  }
}
