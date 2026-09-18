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

  // waitForTab converges on a tab having reached the strip, the same shape
  // DebugPanel.waitForOpen and ActivityQueueDrawer.close use: waitFor with no
  // explicit timeout defers to the enclosing test's own budget, where a fixed
  // cap below that budget fails a merely slow spawn under contention with
  // budget still unused. The env's default tabs land asynchronously after the
  // env open, so every caller that reaches for one of them has to converge on
  // it first -- callers used to hand-roll that wait with their own 15s/20s cap.
  async waitForTab(label: string): Promise<void> {
    await this.tab(label).waitFor({ state: 'visible' });
  }
}
