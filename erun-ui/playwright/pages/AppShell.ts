import type { Locator, Page } from '@playwright/test';
import { ActivityQueueDrawer } from './ActivityQueueDrawer';
import { AIOccupancyPromptDialog } from './AIOccupancyPromptDialog';
import { AutoStartPromptDialog } from './AutoStartPromptDialog';
import { BuildProfileDialog } from './BuildProfileDialog';
import { CloseConfirmDialog } from './CloseConfirmDialog';
import { CreateReviewDialog } from './CreateReviewDialog';
import { DebugPanel } from './DebugPanel';
import { EnvironmentInitDialog } from './EnvironmentInitDialog';
import { GlobalConfigDialog } from './GlobalConfigDialog';
import { ManageDialog } from './ManageDialog';
import { OrchestratorDialog } from './OrchestratorDialog';
import { OutputsDialog } from './OutputsDialog';
import { ReviewDetailDialog } from './ReviewDetailDialog';
import { ReviewPanel } from './ReviewPanel';
import { Sidebar } from './Sidebar';
import { TenantDashboard } from './TenantDashboard';
import { TenantDialog } from './TenantDialog';
import { TerminalPane } from './TerminalPane';
import { TerminalTabStrip } from './TerminalTabStrip';
import { Titlebar } from './Titlebar';

// AppShell is the tests' entry point into the rendered app.
export class AppShell {
  constructor(public readonly page: Page) {}

  // The theme's gating class lives on <html>, not any component the other
  // POMs render, so it is exposed here rather than on Titlebar.
  documentElement(): Locator {
    return this.page.locator('html');
  }

  async open(): Promise<void> {
    await this.page.goto('/');
    await this.page.waitForLoadState('domcontentloaded');
    await this.titlebar.toggleButton().waitFor({ state: 'visible' });
    // The "Loading environments..." overlay clears only once the tenant list
    // is final; wait it out before asserting on sidebar rows, or the check
    // races the still-loading list.
    //
    // This is a settle, not an assertion, so its expiry must not be the thing a
    // test dies on. Hidden is already satisfied when the overlay never rendered
    // (a fast machine). A contended gate is the other case: four workers on a
    // 4-CPU dind keep the overlay up past 30s, and an uncapped wait spends the
    // enclosing budget here -- which reports as "Test timeout of 30000ms
    // exceeded while setting up app" at this line, naming neither the overlay
    // nor the surface under test.
    //
    // The bound must be BELOW whatever budget encloses this call, or the
    // tolerance is unreachable and the budget always wins first. An earlier
    // revision capped at 45s against a 30s test timeout capped nothing: the run
    // died at the row wait below with "Target page ... has been closed", the
    // budget having expired while this wait was still pending. erunApp.ts gives
    // the app fixture 60s of its own, so 40s here leaves that fixture room to
    // reach the row wait -- which IS the assertion that has to hold and fails
    // informatively when the shell genuinely never became usable.
    await this.page
      .getByText('Loading environments...', { exact: true })
      .waitFor({ state: 'hidden', timeout: 40_000 })
      .catch(() => {
        // Expected when the overlay never rendered at all, and tolerated when a
        // contended gate keeps it up past the bound: the row wait below decides.
      });
    await this.page
      .locator(
        'button[aria-label^="Collapse "], button[aria-label^="Expand "], :text("No environments yet")',
      )
      .first()
      .waitFor({ state: 'visible' });
  }

  // reboot re-runs the boot sequence and hands control back as soon as the app
  // chrome is up, for callers that assert on a specific surface boot produces
  // (which session ends up owning the terminal pane). Waiting on that surface is
  // both stricter and faster than open()'s generic settle, whose overlay wait
  // clears only once the pane's session streams its first output.
  async reboot(): Promise<void> {
    await this.page.goto('/');
    await this.page.waitForLoadState('domcontentloaded');
    await this.titlebar.toggleButton().waitFor({ state: 'visible' });
  }

  // reloadEnvironments surfaces a freshly-seeded env deterministically instead
  // of waiting on fsnotify, which can race the watcher's readiness right after
  // boot (see the seededEnv fixture).
  async reloadEnvironments(): Promise<void> {
    await this.page.evaluate(() => {
      (window as unknown as { runtime: { EventsEmit: (name: string) => void } }).runtime.EventsEmit(
        'environments-changed',
      );
    });
  }

  get sidebar(): Sidebar {
    return new Sidebar(this.page);
  }

  get titlebar(): Titlebar {
    return new Titlebar(this.page);
  }

  get globalConfigDialog(): GlobalConfigDialog {
    return new GlobalConfigDialog(this.page);
  }

  get envInitDialog(): EnvironmentInitDialog {
    return new EnvironmentInitDialog(this.page);
  }

  get manageDialog(): ManageDialog {
    return new ManageDialog(this.page);
  }

  get tenantDialog(): TenantDialog {
    return new TenantDialog(this.page);
  }

  get tenantDashboard(): TenantDashboard {
    return new TenantDashboard(this.page);
  }

  get debugPanel(): DebugPanel {
    return new DebugPanel(this.page);
  }

  get reviewPanel(): ReviewPanel {
    return new ReviewPanel(this.page);
  }

  get reviewDetailDialog(): ReviewDetailDialog {
    return new ReviewDetailDialog(this.page);
  }

  get buildProfileDialog(): BuildProfileDialog {
    return new BuildProfileDialog(this.page);
  }

  get createReviewDialog(): CreateReviewDialog {
    return new CreateReviewDialog(this.page);
  }

  get activityDrawer(): ActivityQueueDrawer {
    return new ActivityQueueDrawer(this.page);
  }

  get autoStartPromptDialog(): AutoStartPromptDialog {
    return new AutoStartPromptDialog(this.page);
  }

  get closeConfirmDialog(): CloseConfirmDialog {
    return new CloseConfirmDialog(this.page);
  }

  get aiOccupancyPromptDialog(): AIOccupancyPromptDialog {
    return new AIOccupancyPromptDialog(this.page);
  }

  get orchestratorDialog(): OrchestratorDialog {
    return new OrchestratorDialog(this.page);
  }

  get outputsDialog(): OutputsDialog {
    return new OutputsDialog(this.page);
  }

  get tabStrip(): TerminalTabStrip {
    return new TerminalTabStrip(this.page);
  }

  get terminalPane(): TerminalPane {
    return new TerminalPane(this.page);
  }

  // openEnvironmentTerminal opens an env and settles on its Local session,
  // returning the session id that owns the terminal pane. The env also spawns
  // ERun and AI tabs and each spawn reassigns pane ownership, so all three must
  // be up before a spec reads the id or writes into the pane.
  async openEnvironmentTerminal(tenant: string, environment: string): Promise<number> {
    await this.sidebar.openEnvironment(tenant, environment);
    for (const name of ['Local', 'ERun', 'AI']) {
      await this.tabStrip.waitForTab(name);
    }
    await this.tabStrip.tab('Local').click();
    return this.terminalPane.selectedSessionId();
  }
}
