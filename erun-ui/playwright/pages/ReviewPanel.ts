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
}
