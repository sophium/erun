import type { Locator, Page } from '@playwright/test';

// Build profile detail, opened from a build row in the tenant dashboard or a review.
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
