import type { Locator, Page } from '@playwright/test';

export type TenantDashboardTab =
  | 'Users'
  | 'Reviews'
  | 'Merge queue'
  | 'Builds'
  | 'Audit log'
  | 'API log';

// TenantDashboard POM. Unlike the dialogs, this view replaces the main pane
// content (MainPane.tsx) rather than rendering inside a Radix dialog, so
// waitForOpen anchors on the tab list rather than a role="dialog".
export class TenantDashboard {
  constructor(public readonly page: Page) {}

  async waitForOpen(): Promise<void> {
    await this.page.getByRole('tab', { name: 'Audit log' }).waitFor({ state: 'visible' });
  }

  // waitForOpenRestricted anchors on a tab that survives permission gating, for
  // the cases where the dashboard's other tabs are deliberately absent.
  async waitForOpenRestricted(): Promise<void> {
    await this.page.getByRole('tab', { name: 'API log' }).waitFor({ state: 'visible' });
  }

  async selectTab(name: TenantDashboardTab): Promise<void> {
    await this.page.getByRole('tab', { name }).click();
  }

  tab(name: TenantDashboardTab): Locator {
    return this.page.getByRole('tab', { name });
  }

  tabs(): Locator {
    return this.page.getByRole('tab');
  }

  restrictedAccessNote(): Locator {
    return this.page.getByText('Some panels are hidden because you do not have access to');
  }

  async clickRefresh(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh', exact: true }).click();
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

  reviewsTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  reviewsRows(): Locator {
    return this.reviewsTable().locator('tbody tr');
  }

  reviewsEmptyState(): Locator {
    return this.activePanel().getByText('No reviews yet', { exact: true });
  }

  // reviewsFilteredEmptyState is the distinct "nothing matches this filter"
  // empty state (as opposed to reviewsEmptyState's "nothing exists yet") —
  // the repo's three-empty-states rule requires the two read differently.
  reviewsFilteredEmptyState(): Locator {
    return this.activePanel().getByText('No reviews match this filter', { exact: true });
  }

  clearReviewFilterButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Clear filter' });
  }

  // Mine's accessible name gains a count badge (e.g. "Mine 2"), so this
  // matches the prefix rather than the exact former label (#1378).
  mineFilterButton(): Locator {
    return this.activePanel().getByRole('button', { name: /^Mine\b/ });
  }

  waitingOnMeFilterButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Waiting on me' });
  }

  async openReview(name: string): Promise<void> {
    await this.page.getByRole('button', { name: `Open review ${name}` }).click();
  }

  newReviewButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'New review' });
  }

  reviewsRestrictedNote(): Locator {
    return this.activePanel().getByText('You do not have access to create reviews.');
  }

  mergeQueueTable(): Locator {
    return this.activePanel().getByRole('table');
  }

  mergeQueueRows(): Locator {
    return this.mergeQueueTable().locator('tbody tr');
  }

  advanceMergeQueueButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Advance queue' });
  }

  advanceMergeQueueConfirmButton(): Locator {
    return this.activePanel().getByRole('button', { name: 'Confirm' });
  }

  advanceMergeQueueRestrictedNote(): Locator {
    return this.activePanel().getByText('You do not have access to advance the merge queue.');
  }

  // A queue spanning several target branches has no single head, so the action
  // is replaced by the reason rather than silently absent.
  advanceMergeQueueMixedBranchNote(): Locator {
    return this.activePanel().getByText(
      'These reviews target more than one branch, so there is no single queue head to advance.',
    );
  }
}
