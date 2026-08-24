import type { Locator, Page } from '@playwright/test';

// ReviewPanel is the page object for the right-hand review panel.
export class ReviewPanel {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.locator('aside').filter({ hasText: 'Filter files' }).first();
  }

  async isOpen(): Promise<boolean> {
    const splitter = this.page.getByRole('button', { name: 'Resize diff panel' });
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

  changedFilesTree(): Locator {
    return this.page.getByLabel('Changed files tree');
  }

  async treeFilePaths(): Promise<string[]> {
    return this.changedFilesTree()
      .locator('[data-path]')
      .evaluateAll((els) => els.map((el) => el.getAttribute('data-path') ?? ''));
  }

  async diffSectionPaths(): Promise<string[]> {
    return this.page
      .locator('.diff-file[data-path]')
      .evaluateAll((els) => els.map((el) => el.getAttribute('data-path') ?? ''));
  }

  async collapseDirectory(name: string): Promise<void> {
    await this.page.getByRole('button', { name: `${name} directory` }).click();
  }

  currentTreeNode(): Locator {
    return this.changedFilesTree().locator('[aria-current="true"]');
  }

  // envSectionHeader locates the sticky per-environment header the diff panel
  // renders above each linked environment's section once more than one target
  // is shown (#1178). Scoped to DiffList's sticky header class combination
  // (unique in the frontend source) rather than a plain text match: the
  // changed-files tree renders its own per-env header with the same envKey
  // text, and other surfaces (sidebar rows, tab labels) can also contain it.
  envSectionHeader(envKey: string): Locator {
    return this.page.locator('.sticky.top-0.z-10.border-b').filter({ hasText: envKey });
  }

  // The "Changed files N" collapsible header inside the aside, distinct from
  // the titlebar's "Toggle changed files list" (which hides the whole aside).
  // Collapsing it hides the changed-files tree's own per-env headers and error
  // alerts, so a spec asserting on the diff list's alone can scope past the
  // tree's duplicate rendering of the same slot.
  changedFilesSectionToggle(): Locator {
    return this.page.getByRole('button', { name: /^Changed files/ });
  }

  async collapseChangedFilesSection(): Promise<void> {
    await this.changedFilesSectionToggle().click();
  }

  // The changed-files tree renders its own DiffErrorAlert for the same
  // per-env slot (#1178), so counting alerts document-wide double-counts once
  // both surfaces are visible. Callers that care about the diff list's own
  // alert count should collapseChangedFilesSection() first.
  errorAlerts(): Locator {
    return this.page.getByRole('alert');
  }

  // reviewBoundaryButton locates a "Review layers" scope button by its visible
  // label (e.g. "Current local changes", "All branch changes"). Each linked
  // environment renders its own control with no per-env accessible label, so
  // callers scope by DOM order, which matches the environments' configured
  // order (selectReviewEnvTargets).
  reviewBoundaryButton(label: string, index: number): Locator {
    return this.page.getByRole('button', { name: new RegExp(label) }).nth(index);
  }
}
