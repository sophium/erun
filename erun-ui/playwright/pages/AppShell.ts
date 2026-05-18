import type { Page } from '@playwright/test';
import { Sidebar } from './Sidebar';
import { Titlebar } from './Titlebar';
import { GlobalConfigDialog } from './GlobalConfigDialog';
import { EnvironmentInitDialog } from './EnvironmentInitDialog';
import { ManageDialog } from './ManageDialog';
import { TenantDialog } from './TenantDialog';
import { DebugPanel } from './DebugPanel';
import { ReviewPanel } from './ReviewPanel';
import { ActivityQueueDrawer } from './ActivityQueueDrawer';

// AppShell wraps the top-level rendered app. Tests use it as their entry
// point; component-specific POMs are reached through the accessor properties
// below. open() navigates to '/' and waits until the boot sequence has
// rendered the sidebar's toggle control — that is the latest thing in the
// boot critical path, so its presence implies the rest of the chrome is up.
export class AppShell {
  constructor(public readonly page: Page) {}

  async open(): Promise<void> {
    await this.page.goto('/');
    await this.page.waitForLoadState('domcontentloaded');
    await this.titlebar.toggleButton().waitFor({ state: 'visible' });
    // boot() in TerminalController shows "Loading environments..." in the
    // terminal-busy overlay until LoadState resolves. Wait for that
    // overlay to disappear so the sidebar reflects the final tenant list,
    // and only then accept either the empty-state or the first tenant
    // row.
    await this.page
      .getByText('Loading environments...', { exact: true })
      .waitFor({ state: 'hidden', timeout: 15_000 })
      .catch(() => {
        // The overlay may have already cleared by the time we get here on
        // a fast machine; ignore the timeout in that case and continue.
      });
    await this.page
      .locator(
        'button[aria-label^="Collapse "], button[aria-label^="Expand "], :text("No environments yet")',
      )
      .first()
      .waitFor({ state: 'visible' });
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

  get debugPanel(): DebugPanel {
    return new DebugPanel(this.page);
  }

  get reviewPanel(): ReviewPanel {
    return new ReviewPanel(this.page);
  }

  get activityDrawer(): ActivityQueueDrawer {
    return new ActivityQueueDrawer(this.page);
  }
}
