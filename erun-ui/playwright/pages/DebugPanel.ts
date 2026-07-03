import type { Locator, Page } from '@playwright/test';

// Diagnostics console POM. The panel sits at the bottom of the
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

  // erunTraceClearButton scopes to the erun-trace pane's grid (toolbar +
  // scroll body share a parent), disambiguating from other "Clear" buttons
  // elsewhere in the app (e.g. the activity panel) when an environment is open.
  erunTraceClearButton(): Locator {
    return this.erunTracePane().locator('..').getByRole('button', { name: 'Clear', exact: true });
  }

  // erunTraceShowAllButton is the "Show all" affordance in the since-cleared
  // notice row, scoped to the same erun-trace grid.
  erunTraceShowAllButton(): Locator {
    return this.erunTracePane().locator('..').getByRole('button', { name: 'Show all' });
  }

  copyReportButton(): Locator {
    return this.page.getByRole('button', { name: /^(Copy report|Copied|Copy failed)$/ });
  }
}
