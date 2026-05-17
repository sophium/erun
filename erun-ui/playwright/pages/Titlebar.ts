import type { Locator, Page } from '@playwright/test';

// Titlebar POM. The titlebar exposes layout-toggle controls and the
// long-running status banner (Nielsen #1 visibility-of-system-status).
export class Titlebar {
  constructor(public readonly page: Page) {}

  toggleButton(): Locator {
    return this.page.getByRole('button', { name: 'Toggle sidebar' });
  }

  async toggleSidebar(): Promise<void> {
    await this.toggleButton().click();
  }

  async toggleReviewPanel(): Promise<void> {
    await this.page.getByRole('button', { name: 'Toggle diff panel' }).click();
  }

  async toggleFilesPanel(): Promise<void> {
    await this.page.getByRole('button', { name: 'Toggle changed files list' }).click();
  }

  async openVSCode(): Promise<void> {
    // The aria-label is computed from the active selection so we match by
    // role + name-startsWith via a regex.
    await this.page.getByRole('button', { name: /VS Code/i }).first().click();
  }

  async openIntelliJ(): Promise<void> {
    await this.page.getByRole('button', { name: /IntelliJ|IDEA/i }).first().click();
  }

  async dismissStatus(): Promise<void> {
    const dismiss = this.page.getByRole('button', { name: 'Dismiss status' });
    if (await dismiss.isVisible().catch(() => false)) {
      await dismiss.click();
    }
  }

  statusMessage(): Locator {
    // role="status" or role="alert" with aria-live, depending on the
    // terminalMessage kind. The titlebar wraps both in the same banner.
    return this.page
      .locator('[role="status"][aria-live="polite"], [role="alert"][aria-live="assertive"]')
      .first();
  }
}
