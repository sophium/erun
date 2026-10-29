import type { Locator, Page } from '@playwright/test';

// Scoped to the titlebar <header>: TerminalBusyOverlay renders a same-shaped status
// node ("Opening <tenant> / <environment>...") while a session opens.
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

  // Render only when the active session is an environment tab.
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

  // Global, so it renders whichever session tab is active.
  whipButton(): Locator {
    return this.page.getByRole('button', { name: /^Whip:/ });
  }

  whipReportHeading(): Locator {
    return this.page.getByRole('heading', { name: 'Whip' });
  }

  // Gates on the report body, not the heading, which stays mounted across both views.
  async waitForWhipReportOpen(): Promise<void> {
    await this.whipReportBody().waitFor({ state: 'visible' });
  }

  // The exit animation runs on the real clock even under a fake timer clock.
  async waitForWhipReportClosed(): Promise<void> {
    await this.whipReportHeading().waitFor({ state: 'hidden' });
  }

  // Scoped to the popover: the same names render elsewhere on the page.
  whipPanel(): Locator {
    return this.page.getByRole('region', { name: 'Whip' });
  }

  async openWhipPanel(): Promise<void> {
    await this.whipButton().click();
    await this.whipPanel().waitFor({ state: 'visible' });
  }

  // Scoped to the popover: the same names render elsewhere on the page.
  whipReportBody(): Locator {
    return this.page.getByRole('status', { name: 'Whip results' });
  }

  async closeWhipReport(): Promise<void> {
    await this.page.getByRole('button', { name: 'Close whip' }).click();
    await this.whipReportHeading().waitFor({ state: 'hidden' });
  }

  whipTargetCheckbox(name: string): Locator {
    return this.whipPanel().getByRole('checkbox', { name });
  }

  // `exact`: the third name starts with the first, and a non-exact name would match both.
  selectAllOrchestratorsButton(): Locator {
    return this.whipPanel().getByRole('button', { name: 'Select all orchestrators', exact: true });
  }

  selectAllEnvironmentsButton(): Locator {
    return this.whipPanel().getByRole('button', { name: 'Select all environments', exact: true });
  }

  selectAllButton(): Locator {
    return this.whipPanel().getByRole('button', {
      name: 'Select all orchestrators and environments',
      exact: true,
    });
  }

  // The label states the resolved count, so match its prefix.
  whipRunButton(): Locator {
    return this.whipPanel().getByRole('button', { name: /^Whip(ping…| \d+ target)/ });
  }

  async toggleTheme(): Promise<void> {
    await this.themeToggleButton().click();
  }

  async openVSCode(): Promise<void> {
    // The aria-label depends on the selection.
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

  // A banner can be replaced between locator polls; the MutationObserver sees every line.
  // Call before triggering the flow, then assert with sawBanner.
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

  // Mounts after the first idle poll. Unconfirmed readings lead with a caveat, so
  // "Idle timeout" is matched anywhere in the name.
  idleStatusBadge(): Locator {
    return this.page.getByRole('button', { name: /Idle timeout/ });
  }

  idleTransitionPill(): Locator {
    return this.page.getByTestId('titlebar-idle-transition');
  }

  cloudNodePowerButton(label: string | RegExp): Locator {
    return this.page.getByRole('button', { name: label });
  }

  // 'debug' has no titlebar icon; it is reachable only from inside the dialog.
  messageCenterIcon(kind: 'error' | 'warning' | 'info' | 'success'): Locator {
    const label = kind.charAt(0).toUpperCase() + kind.slice(1);
    return this.page.getByRole('button', { name: new RegExp(`^${label}: \\d+ unread$`) });
  }

  messageCenterIconBadge(kind: 'error' | 'warning' | 'info' | 'success'): Locator {
    return this.messageCenterIcon(kind).locator('span');
  }

  async openMessageCenter(kind: 'error' | 'warning' | 'info' | 'success'): Promise<void> {
    await this.messageCenterIcon(kind).click();
    await this.messageCenterDialog().waitFor({ state: 'visible' });
  }

  // Renders only while nothing at all is unread; specs that only cleared their own
  // entry point should use messageCenterEntryPoint.
  messageCenterHistoryButton(): Locator {
    return this.page.getByRole('button', { name: 'Message history' });
  }

  // The titlebar offers exactly one entry point: the history fallback while nothing is
  // unread, else an unread class icon. Earlier specs may leave a class unread.
  messageCenterEntryPoint(): Locator {
    return this.messageCenterIcon('error')
      .or(this.messageCenterIcon('warning'))
      .or(this.messageCenterIcon('info'))
      .or(this.messageCenterIcon('success'))
      .or(this.messageCenterHistoryButton())
      .first();
  }

  // A class icon opens the dialog filtered to that class, so switch to All.
  async openMessageCenterFromTitlebar(): Promise<void> {
    await this.messageCenterEntryPoint().click();
    await this.messageCenterDialog().waitFor({ state: 'visible' });
    await this.messageCenterTab('All').click();
  }

  messageCenterDialog(): Locator {
    return this.page.getByRole('dialog', { name: 'Messages' });
  }

  messageCenterTab(name: string): Locator {
    return this.messageCenterDialog().getByRole('tab', { name, exact: true });
  }

  messageCenterShowDebugToggle(): Locator {
    return this.messageCenterDialog().getByRole('checkbox', { name: 'Show debug messages' });
  }

  messageCenterMarkClassReadButton(kind: 'error' | 'warning' | 'info' | 'success'): Locator {
    return this.messageCenterDialog().getByRole('button', {
      name: new RegExp(`^Mark ${kind} read$`),
    });
  }

  messageCenterMarkAllReadButton(): Locator {
    return this.messageCenterDialog().getByRole('button', { name: 'Mark all read', exact: true });
  }

  messageCenterRow(messageText: string | RegExp): Locator {
    return this.messageCenterDialog().getByRole('listitem').filter({ hasText: messageText });
  }

  async closeMessageCenter(): Promise<void> {
    await this.page.keyboard.press('Escape');
    await this.messageCenterDialog().waitFor({ state: 'hidden' });
  }
}
