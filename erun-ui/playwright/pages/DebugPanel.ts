import type { Locator, Page } from '@playwright/test';

// Diagnostics console POM.
export class DebugPanel {
  constructor(public readonly page: Page) {}

  resizeHandle(): Locator {
    return this.page.getByRole('slider', { name: 'Resize diagnostics panel' });
  }

  toggleButton(): Locator {
    // The accessible name changes with open/closed state but always starts
    // with "Diagnostics", so match the prefix rather than an exact name.
    return this.page.getByRole('button', { name: /^Diagnostics\b/ }).first();
  }

  async toggle(): Promise<void> {
    await this.toggleButton().click();
  }

  async isOpen(): Promise<boolean> {
    return (await this.resizeHandle().count()) > 0 && (await this.resizeHandle().isVisible());
  }

  tab(name: 'erun trace' | 'orchestrator' | 'app log' | 'UI trace'): Locator {
    return this.page.getByRole('tab', { name });
  }

  async selectTab(name: 'erun trace' | 'orchestrator' | 'app log' | 'UI trace'): Promise<void> {
    await this.tab(name).click();
  }

  erunTracePane(): Locator {
    return this.page.getByLabel('erun trace output');
  }

  // The orchestrator context's own identity block (name, status, linked
  // environments) sits above its app-log tail (orchestratorLogPane()).
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

  // Scope to the erun-trace grid: other "Clear" buttons exist elsewhere
  // (e.g. the activity panel) when an environment is open.
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
