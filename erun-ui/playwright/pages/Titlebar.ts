import type { Locator, Page } from '@playwright/test';

// One banner rendered as status or alert depending on the terminalMessage kind.
const TITLEBAR_BANNER_SELECTOR =
  '[role="status"][aria-live="polite"], [role="alert"][aria-live="assertive"]';

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
    return this.page.locator(TITLEBAR_BANNER_SELECTOR).first();
  }

  // The titlebar renders one banner at a time, so a line can be replaced before a
  // locator poll samples it — a success line followed by a failure line for the
  // same flow is the common case. recordBanners installs a MutationObserver that
  // fires on the mutation itself, so no rendered line can slip between samples;
  // call it before triggering the flow, then assert with sawBanner. Use this
  // instead of statusMessage() whenever another banner can follow the asserted one.
  async recordBanners(): Promise<void> {
    await this.page.evaluate((selector) => {
      const target = window as unknown as { __erunBannerLog?: string[] };
      const log: string[] = [];
      target.__erunBannerLog = log;
      const capture = (): void => {
        document.querySelectorAll(selector).forEach((node) => {
          const text = node.textContent?.trim() ?? '';
          if (text && log[log.length - 1] !== text) {
            log.push(text);
          }
        });
      };
      capture();
      new MutationObserver(capture).observe(document.body, {
        subtree: true,
        childList: true,
        characterData: true,
      });
    }, TITLEBAR_BANNER_SELECTOR);
  }

  async sawBanner(expected: string): Promise<boolean> {
    const banners = await this.page.evaluate(
      () => (window as unknown as { __erunBannerLog?: string[] }).__erunBannerLog ?? [],
    );
    return banners.some((banner) => banner.includes(expected));
  }

  // The pill only mounts after the first idle-status poll completes for the selected env, so callers must wait for visibility before driving it.
  idleStatusBadge(): Locator {
    return this.page.getByRole('button', { name: /^Idle timeout/ });
  }
}
