import type { Locator, Page } from '@playwright/test';

// Lists orchestrators when an orchestrator session owns the pane, else the env's terminals.
export class TerminalTabStrip {
  constructor(private readonly page: Page) {}

  orchestratorMode(): Locator {
    return this.page.getByRole('tablist', { name: 'Orchestrators' });
  }

  environmentMode(): Locator {
    return this.page.getByRole('tablist', { name: 'Open terminals' });
  }

  tab(label: string): Locator {
    return this.page.getByRole('tab', { name: label, exact: true });
  }

  // No explicit timeout, so the enclosing test's budget bounds the wait.
  async waitForTab(label: string): Promise<void> {
    await this.tab(label).waitFor({ state: 'visible' });
  }

  // On an orchestrator tab this stops the orchestrator.
  async closeTab(label: string): Promise<void> {
    await this.page.getByRole('button', { name: `Close ${label}`, exact: true }).click();
  }
}
