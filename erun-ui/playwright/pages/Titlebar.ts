import type { Locator, Page } from '@playwright/test';

// One banner rendered as status or alert depending on the terminalMessage
// kind. Scoped to the titlebar's own <header> (Titlebar.tsx) because
// TerminalBusyOverlay renders an unrelated role="status" aria-live="polite"
// node over the terminal pane with the same signature -- an unscoped
// selector's .first() can resolve to that overlay's "Opening <tenant> /
// <environment>..." text instead of the titlebar's own banner whenever a
// session is mid-open, which is exactly the default env this harness
// auto-opens on every boot.
const TITLEBAR_BANNER_SELECTOR =
  'header [role="status"][aria-live="polite"], header [role="alert"][aria-live="assertive"]';

export class Titlebar {
  constructor(public readonly page: Page) {}

  toggleButton(): Locator {
    return this.page.getByRole('button', { name: 'Toggle sidebar' });
  }

  async toggleSidebar(): Promise<void> {
    await this.toggleButton().click();
  }

  async toggleReviewPanel(): Promise<void> {
    await this.diffPanelToggle().click();
  }

  diffPanelToggle(): Locator {
    return this.page.getByRole('button', { name: 'Toggle diff panel' });
  }

  async toggleFilesPanel(): Promise<void> {
    await this.changedFilesToggle().click();
  }

  changedFilesToggle(): Locator {
    return this.page.getByRole('button', { name: 'Toggle changed files list' });
  }

  // Env-scoped titlebar controls: the two IDE buttons and the contribute
  // toggle. These render only when the active session is an environment tab,
  // not an orchestrator session (#1178).
  vscodeButton(): Locator {
    return this.page.getByRole('button', { name: /VS Code/i });
  }

  intellijButton(): Locator {
    return this.page.getByRole('button', { name: /IntelliJ|IDEA/i });
  }

  contributeToggleButton(): Locator {
    return this.page.getByRole('button', { name: /Contribute to ERun|Disable contribute mode/ });
  }

  themeToggleButton(): Locator {
    return this.page.getByRole('button', { name: /Switch to (light|dark) theme/ });
  }

  // The whip control is global (every orchestrator, every environment), so
  // unlike the env-scoped controls above it renders regardless of which
  // session tab is active.
  whipButton(): Locator {
    return this.page.getByRole('button', { name: /^Whip:/ });
  }

  whipReportHeading(): Locator {
    return this.page.getByRole('heading', { name: 'Whip' });
  }

  // Scoped to the popover's own live region: several seeded rows (the
  // sidebar row, the terminal tab) already render the same env/orchestrator
  // name elsewhere on the page, so an unscoped getByText(name) is ambiguous.
  whipReportBody(): Locator {
    return this.page.getByRole('status', { name: 'Whip results' });
  }

  async closeWhipReport(): Promise<void> {
    await this.page.getByRole('button', { name: 'Close whip report' }).click();
  }

  async toggleTheme(): Promise<void> {
    await this.themeToggleButton().click();
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

  // Scoped to the titlebar's own alert banner (see TITLEBAR_BANNER_SELECTOR's
  // header-scoping comment) so this never resolves to an unrelated alert
  // elsewhere on the page (a dialog's InlineAlert, a panel's own role="alert").
  errorAlert(): Locator {
    return this.page.locator('header [role="alert"]');
  }

  reportBugButton(): Locator {
    return this.errorAlert().getByRole('button', { name: /^Report a bug/ });
  }

  deployActionButton(): Locator {
    return this.errorAlert().getByRole('button', { name: 'Deploy', exact: true });
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

  // The pill only mounts after the first idle-status poll completes for the
  // selected env, so callers must wait for visibility before driving it. The
  // accessible name normally starts with "Idle timeout", but a reading the
  // pod never confirmed leads with a provenance caveat instead — match
  // "Idle timeout" anywhere in the name so both cases resolve the same badge.
  idleStatusBadge(): Locator {
    return this.page.getByRole('button', { name: /Idle timeout/ });
  }
}
