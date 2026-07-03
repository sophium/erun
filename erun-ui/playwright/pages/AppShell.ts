import type { Page } from '@playwright/test';
import { ActivityQueueDrawer } from './ActivityQueueDrawer';
import { AutoStartPromptDialog } from './AutoStartPromptDialog';
import { DebugPanel } from './DebugPanel';
import { EnvironmentInitDialog } from './EnvironmentInitDialog';
import { GlobalConfigDialog } from './GlobalConfigDialog';
import { ManageDialog } from './ManageDialog';
import { OutputsDialog } from './OutputsDialog';
import { ReviewPanel } from './ReviewPanel';
import { Sidebar } from './Sidebar';
import { TenantDialog } from './TenantDialog';
import { Titlebar } from './Titlebar';

// AppShell is the tests' entry point into the rendered app.
export class AppShell {
  constructor(public readonly page: Page) {}

  async open(): Promise<void> {
    await this.page.goto('/');
    await this.page.waitForLoadState('domcontentloaded');
    await this.titlebar.toggleButton().waitFor({ state: 'visible' });
    // The "Loading environments..." overlay clears only once the tenant list
    // is final; wait it out before asserting on sidebar rows, or the check
    // races the still-loading list.
    await this.page
      .getByText('Loading environments...', { exact: true })
      .waitFor({ state: 'hidden', timeout: 15_000 })
      .catch(() => {
        // The overlay may already be gone on a fast machine, so the timeout
        // here is expected rather than a failure.
      });
    await this.page
      .locator(
        'button[aria-label^="Collapse "], button[aria-label^="Expand "], :text("No environments yet")',
      )
      .first()
      .waitFor({ state: 'visible' });
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

  get debugPanel(): DebugPanel {
    return new DebugPanel(this.page);
  }

  get reviewPanel(): ReviewPanel {
    return new ReviewPanel(this.page);
  }

  get activityDrawer(): ActivityQueueDrawer {
    return new ActivityQueueDrawer(this.page);
  }

  get autoStartPromptDialog(): AutoStartPromptDialog {
    return new AutoStartPromptDialog(this.page);
  }

  get outputsDialog(): OutputsDialog {
    return new OutputsDialog(this.page);
  }
}
