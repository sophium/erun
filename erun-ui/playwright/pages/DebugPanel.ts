import type { Locator, Page } from '@playwright/test';

// Diagnostics console POM (issue #466). The panel sits at the bottom of the
// terminal area and toggles open/closed; when open it renders a "Resize
// diagnostics panel" handle, an "erun trace" / "UI trace" tab pair, and
// per-pane Refresh/Copy/Clear actions.
export class DebugPanel {
  constructor(public readonly page: Page) {}

  resizeHandle(): Locator {
    return this.page.getByRole('button', { name: 'Resize diagnostics panel' });
  }

  toggleButton(): Locator {
    // The toggle is the section header button. Its accessible name is
    // "Diagnostics collapsed" when closed and "Diagnostics erun trace +
    // UI trace" when open; both start with "Diagnostics".
    return this.page.getByRole('button', { name: /^Diagnostics\b/ }).first();
  }

  async toggle(): Promise<void> {
    await this.toggleButton().click();
  }

  async isOpen(): Promise<boolean> {
    return (await this.resizeHandle().count()) > 0 && (await this.resizeHandle().isVisible());
  }

  tab(name: 'erun trace' | 'UI trace'): Locator {
    return this.page.getByRole('tab', { name });
  }

  async selectTab(name: 'erun trace' | 'UI trace'): Promise<void> {
    await this.tab(name).click();
  }

  erunTracePane(): Locator {
    return this.page.getByLabel('erun trace output');
  }

  uiTracePane(): Locator {
    return this.page.getByLabel('UI trace output');
  }

  refreshButton(): Locator {
    return this.page.getByRole('button', { name: 'Refresh' });
  }

  copyButton(): Locator {
    return this.page.getByRole('button', { name: /^(Copy|Copied|Copy failed)$/ });
  }

  clearButton(): Locator {
    return this.page.getByRole('button', { name: 'Clear' });
  }

  enableDebugOutputButton(): Locator {
    return this.page.getByRole('button', { name: /^(Enable debug output|Enabling…)$/ });
  }
}
