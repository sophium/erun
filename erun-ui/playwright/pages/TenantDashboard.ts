import type { Locator, Page } from '@playwright/test';

export type TenantDashboardTab = 'Users' | 'Merge queue' | 'Builds' | 'Audit log' | 'API log';

// TenantDashboard POM. Unlike the dialogs, this view replaces the main pane
// content (MainPane.tsx) rather than rendering inside a Radix dialog, so
// waitForOpen anchors on the tab list rather than a role="dialog".
export class TenantDashboard {
  constructor(public readonly page: Page) {}

  async waitForOpen(): Promise<void> {
    await this.page.getByRole('tab', { name: 'Audit log' }).waitFor({ state: 'visible' });
  }

  async selectTab(name: TenantDashboardTab): Promise<void> {
    await this.page.getByRole('tab', { name }).click();
  }

  activePanel(): Locator {
    return this.page.getByRole('tabpanel');
  }

  auditTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  auditRows(): Locator {
    return this.auditTable().locator('tbody tr');
  }

  auditEmptyState(): Locator {
    return this.activePanel().getByText('No audit events', { exact: true });
  }
}
