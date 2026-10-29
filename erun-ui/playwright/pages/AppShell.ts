import type { Locator, Page } from '@playwright/test';
import { ActivityQueueDrawer } from './ActivityQueueDrawer';
import { AIOccupancyPromptDialog } from './AIOccupancyPromptDialog';
import { AutoStartPromptDialog } from './AutoStartPromptDialog';
import { BuildProfileDialog } from './BuildProfileDialog';
import { CloseConfirmDialog } from './CloseConfirmDialog';
import { CreateReviewDialog } from './CreateReviewDialog';
import { DebugPanel } from './DebugPanel';
import { EnvironmentInitDialog } from './EnvironmentInitDialog';
import { GatewayRedeployDialog } from './GatewayRedeployDialog';
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

export class AppShell {
  constructor(public readonly page: Page) {}

  // The theme class lives on <html>, outside any component POM.
  documentElement(): Locator {
    return this.page.locator('html');
  }

  async open(): Promise<void> {
    await this.page.goto('/');
    await this.page.waitForLoadState('domcontentloaded');
    await this.titlebar.toggleButton().waitFor({ state: 'visible' });
    // Settle, not assertion: the row wait below is what must hold. The 40s cap stays below the
    // app fixture's 60s (erunApp.ts) so a contended overlay cannot eat the whole budget.
    await this.page
      .getByText('Loading environments...', { exact: true })
      .waitFor({ state: 'hidden', timeout: 40_000 })
      .catch(() => {
        // Tolerated: the row wait below decides.
      });
    await this.page
      .locator(
        'button[aria-label^="Collapse "], button[aria-label^="Expand "], :text("No environments yet")',
      )
      .first()
      .waitFor({ state: 'visible' });
  }

  // reboot returns once the chrome is up, for callers that wait on a specific boot surface.
  async reboot(): Promise<void> {
    await this.page.goto('/');
    await this.page.waitForLoadState('domcontentloaded');
    await this.titlebar.toggleButton().waitFor({ state: 'visible' });
  }

  // reloadEnvironments avoids racing fsnotify right after boot.
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

  get gatewayRedeployDialog(): GatewayRedeployDialog {
    return new GatewayRedeployDialog(this.page);
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

  // openEnvironmentTerminal waits for Local, ERun and AI tabs, since each spawn reassigns
  // pane ownership, then returns the owning session id.
  async openEnvironmentTerminal(tenant: string, environment: string): Promise<number> {
    await this.sidebar.openEnvironment(tenant, environment);
    for (const name of ['Local', 'ERun', 'AI']) {
      await this.tabStrip.waitForTab(name);
    }
    await this.tabStrip.tab('Local').click();
    return this.terminalPane.selectedSessionId();
  }
}
