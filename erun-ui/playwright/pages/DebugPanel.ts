import type { Locator, Page } from '@playwright/test';

// DebugPanel POM. The debug panel sits at the bottom of the terminal area
// and toggles open/closed; when open it renders a "Resize debug panel"
// handle button and Copy/Clear actions.
export class DebugPanel {
  constructor(public readonly page: Page) {}

  resizeHandle(): Locator {
    return this.page.getByRole('button', { name: 'Resize debug panel' });
  }

  toggleButton(): Locator {
    // The toggle is the section header button. Its accessible name is
    // "Debug collapsed" when the panel is closed and "Debug erun -vv
    // output" when open; both start with "Debug ".
    return this.page.getByRole('button', { name: /^Debug\b/ }).first();
  }

  async toggle(): Promise<void> {
    await this.toggleButton().click();
  }

  async isOpen(): Promise<boolean> {
    return (await this.resizeHandle().count()) > 0 && (await this.resizeHandle().isVisible());
  }

  async clear(): Promise<void> {
    await this.page.getByRole('button', { name: /^Clear/ }).click();
  }

  async copy(): Promise<void> {
    await this.page.getByRole('button', { name: /^(Copy|Copied)/ }).click();
  }
}
