import type { Locator, Page } from '@playwright/test';

// ReviewPanel POM. The right-hand review panel surfaces the diff list and
// the filter input; visibility is gated on layout.reviewOpen.
export class ReviewPanel {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    // The review panel uses the "Filter files..." placeholder when its
    // changed-files section is open; that input is the most reliable
    // entry-point to find the surface.
    return this.page.locator('aside').filter({ hasText: 'Filter files' }).first();
  }

  async isOpen(): Promise<boolean> {
    const splitter = this.page.locator('[role="separator"][aria-label="Resize diff panel"]');
    if (!(await splitter.count())) return false;
    return splitter.first().isVisible();
  }

  filterInput(): Locator {
    return this.page.getByPlaceholder('Filter files...');
  }

  async getDiffFilter(): Promise<string> {
    if (!(await this.filterInput().count())) return '';
    return (await this.filterInput().inputValue()) || '';
  }

  async setDiffFilter(filter: string): Promise<void> {
    await this.filterInput().fill(filter);
  }

  async toggleChangedFiles(): Promise<void> {
    await this.page.getByRole('button', { name: 'Toggle changed files list' }).click();
  }
}
