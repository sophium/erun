import type { Locator, Page } from '@playwright/test';

export class DebugPanel {
  constructor(public readonly page: Page) {}

  resizeHandle(): Locator {
    return this.page.getByRole('slider', { name: 'Resize diagnostics panel' });
  }

  toggleButton(): Locator {
    // The name varies with state but always starts with "Diagnostics".
    return this.page.getByRole('button', { name: /^Diagnostics\b/ }).first();
  }

  async toggle(): Promise<void> {
    await this.toggleButton().click();
  }

  async isOpen(): Promise<boolean> {
    return (await this.resizeHandle().count()) > 0 && (await this.resizeHandle().isVisible());
  }

  // Waits on the panel's render, under the test's own budget.
  async waitForOpen(): Promise<void> {
    await this.resizeHandle().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.resizeHandle().waitFor({ state: 'hidden' });
  }

  tab(name: 'erun trace' | 'orchestrator' | 'app log' | 'UI trace'): Locator {
    return this.page.getByRole('tab', { name });
  }

  // The primary tab renames itself with the context, so the label names tab and pane.
  paneFor(name: 'erun trace' | 'orchestrator' | 'app log' | 'UI trace'): Locator {
    switch (name) {
      case 'orchestrator':
        return this.orchestratorPane();
      case 'app log':
        return this.appLogPane();
      case 'UI trace':
        return this.uiTracePane();
      default:
        return this.erunTracePane();
    }
  }

  // Waits on the revealed pane, under the test's own budget.
  async selectTab(name: 'erun trace' | 'orchestrator' | 'app log' | 'UI trace'): Promise<void> {
    await this.tab(name).click();
    await this.paneFor(name).waitFor({ state: 'visible' });
  }

  erunTracePane(): Locator {
    return this.page.getByLabel('erun trace output');
  }

  orchestratorPane(): Locator {
    return this.page.getByLabel('orchestrator diagnostics');
  }

  orchestratorLogPane(): Locator {
    return this.page.getByLabel('orchestrator output');
  }

  appLogPane(): Locator {
    return this.page.getByLabel('app log output');
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

  // Other "Clear" buttons exist when an environment is open.
  erunTraceClearButton(): Locator {
    return this.erunTracePane().locator('..').getByRole('button', { name: 'Clear', exact: true });
  }

  erunTraceShowAllButton(): Locator {
    return this.erunTracePane().locator('..').getByRole('button', { name: 'Show all' });
  }

  copyReportButton(): Locator {
    return this.page.getByRole('button', { name: /^(Copy report|Copied|Copy failed)$/ });
  }

  reportIssueButton(): Locator {
    return this.page.getByRole('button', {
      name: /^(Report an erun issue|Opened — full report copied|Could not open issue)$/,
    });
  }
}
