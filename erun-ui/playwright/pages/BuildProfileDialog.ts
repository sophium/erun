import type { Locator, Page } from '@playwright/test';

// BuildProfileDialog is the "select a build, see what consumed CPU or hit an
// I/O bottleneck" detail surface (root AGENTS.md #2274), opened from a
// "View build profile for <buildId>" button on a build row in either the
// tenant dashboard's Builds tab (TenantDashboard POM) or a review's own
// build list (ReviewDetailDialog POM). It renders as a Radix Dialog named by
// its own DialogTitle ("Build profile"), so it stays unambiguous even when
// nested inside ReviewDetailDialog's own dialog.
export class BuildProfileDialog {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.getByRole('dialog', { name: 'Build profile' });
  }

  async waitForOpen(): Promise<void> {
    await this.locator().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.locator().waitFor({ state: 'hidden' });
  }

  noProfileEmptyState(): Locator {
    return this.locator().getByText('No profile collected for this build', { exact: true });
  }

  notAvailableNotice(): Locator {
    return this.locator().getByText('CPU and I/O metrics are not available for this build.', {
      exact: true,
    });
  }

  stepsTable(): Locator {
    return this.locator().getByRole('table');
  }

  stepRows(): Locator {
    return this.stepsTable().locator('tbody tr');
  }

  truncatedNote(): Locator {
    return this.locator().getByText('not shown (showing the', { exact: false });
  }
}
