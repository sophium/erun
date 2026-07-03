import type { Locator, Page } from '@playwright/test';

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
    // The aria-label is computed from the active selection, so there is no fixed name to match exactly.
    await this.page
      .getByRole('button', { name: /VS Code/i })
      .first()
      .click();
  }

  async openIntelliJ(): Promise<void> {
    await this.page
      .getByRole('button', { name: /IntelliJ|IDEA/i })
      .first()
      .click();
  }

  async dismissStatus(): Promise<void> {
    const dismiss = this.page.getByRole('button', { name: 'Dismiss status' });
    if (await dismiss.isVisible().catch(() => false)) {
      await dismiss.click();
    }
  }

  statusMessage(): Locator {
    // One banner rendered as status or alert depending on the terminalMessage kind, so match either.
    return this.page
      .locator('[role="status"][aria-live="polite"], [role="alert"][aria-live="assertive"]')
      .first();
  }

  // The pill only mounts after the first idle-status poll completes for the selected env, so callers must wait for visibility before driving it.
  idleStatusBadge(): Locator {
    return this.page.getByRole('button', { name: /^Idle timeout/ });
  }
}
