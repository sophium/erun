import type { Locator, Page } from '@playwright/test';

// AutoStartPromptDialog POM. The dialog opens from openSelection when the
// clicked env is remote, has a stopped cloud context, and has no AutoStart
// override on file yet (state.tenants[].autoStart === undefined). It is the
// only dialog whose title starts with "Auto-start ", so the role match is
// unambiguous even if other dialogs are mounted.
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
