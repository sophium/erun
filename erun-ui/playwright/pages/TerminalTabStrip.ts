import type { Locator, Page } from '@playwright/test';

// TerminalTabStrip is the strip above the terminal pane. Which of its two modes
// is mounted is the rendered answer to "what owns the pane": an orchestrator
// session (cross-env, so the strip lists orchestrators) or the selected
// environment's terminals.
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
}
