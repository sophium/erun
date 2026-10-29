import type { Locator, Page } from '@playwright/test';

export class ReviewPanel {
  constructor(public readonly page: Page) {}

  locator(): Locator {
    return this.page.locator('aside').filter({ hasText: 'Filter files' }).first();
  }

  resizeHandle(): Locator {
    return this.page.getByRole('slider', { name: 'Resize diff panel' });
  }

  async isOpen(): Promise<boolean> {
    const splitter = this.resizeHandle();
    if (!(await splitter.count())) return false;
    return splitter.first().isVisible();
  }

  // Toggle and render are separate React steps; wait on the panel itself before touching its content.
  async waitForOpen(): Promise<void> {
    await this.resizeHandle().waitFor({ state: 'visible' });
  }

  async waitForClosed(): Promise<void> {
    await this.resizeHandle().waitFor({ state: 'hidden' });
  }

  diffContentRegion(): Locator {
    return this.page.getByRole('region', { name: 'Diff content' });
  }

  hunkRegion(filePath: string): Locator {
    return this.page.getByRole('region', { name: new RegExp(`^Diff for ${filePath} `) });
  }

  hunkRegionAt(filePath: string, header: string): Locator {
    return this.page.getByRole('region', {
      name: `Diff for ${filePath} at ${header}`,
      exact: true,
    });
  }

  keyboardShortcutsButton(): Locator {
    return this.page.getByRole('button', { name: 'Keyboard shortcuts' });
  }

  // Scoped to the popover: "Start a review" is also a real button label in the panel.
  keyboardShortcutsPopover(): Locator {
    return this.page.locator('[data-slot="popover-content"]');
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

  // Scoped to the tree so it doesn't match other copies of the same label.
  changedFilesEnvLabel(envKey: string): Locator {
    const [tenant, environment] = envKey.split('/');
    return this.changedFilesTree().getByText(`${tenant} / ${environment}`, { exact: true });
  }

  envLabels(envKey: string): Locator {
    const [tenant, environment] = envKey.split('/');
    return this.page.getByText(`${tenant} / ${environment}`, { exact: true });
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

  // The scrollspy update is coalesced onto the next animation frame, so the active
  // file lags the scroll; true once the region scrolled and the active file is visible.
  async diffScrollSelectionSettled(): Promise<boolean> {
    return this.page.evaluate(() => {
      const region = document.querySelector<HTMLElement>(
        '[role="region"][aria-label="Diff content"]',
      );
      const tree = document.querySelector<HTMLElement>('[aria-label="Changed files tree"]');
      if (!region || !tree || region.scrollTop <= 0) {
        return false;
      }
      const path = tree
        .querySelector<HTMLElement>('[aria-current="true"]')
        ?.getAttribute('data-path');
      if (!path) {
        return false;
      }
      const section = region.querySelector<HTMLElement>(
        `.diff-file[data-path="${CSS.escape(path)}"]`,
      );
      if (!section) {
        return false;
      }
      const regionBox = region.getBoundingClientRect();
      const sectionBox = section.getBoundingClientRect();
      return sectionBox.top < regionBox.bottom && sectionBox.bottom > regionBox.top;
    });
  }

  // Rendered label spaces the slash ("tenant / environment").
  envSectionHeader(envKey: string): Locator {
    const [tenant, environment] = envKey.split('/');
    return this.page
      .locator('.sticky.top-0.z-10.border-b')
      .filter({ hasText: `${tenant} / ${environment}` });
  }

  // `.sticky` excludes the per-file sections that also carry data-env-key.
  envActionHeader(envKey: string): Locator {
    return this.page.locator(`[data-env-key="${envKey}"].sticky`);
  }

  // Exact match: a substring could hit a longer label ("Ready" in "Already").
  reviewStatusChip(envKey: string, label: string): Locator {
    return this.envActionHeader(envKey).getByText(label, { exact: true });
  }

  reviewActionButton(envKey: string, name: string | RegExp): Locator {
    return this.envActionHeader(envKey).getByRole('button', { name });
  }

  changedFilesSectionToggle(): Locator {
    return this.page.getByRole('button', { name: /^Changed files/ });
  }

  async collapseChangedFilesSection(): Promise<void> {
    await this.changedFilesSectionToggle().click();
  }

  // Scoped to the diff region: sidebar notices are also role="alert".
  errorAlerts(): Locator {
    return this.diffContentRegion().getByRole('alert');
  }

  // role="status", not alert: not-running is not a fault.
  reachabilityStatuses(): Locator {
    return this.diffContentRegion().getByRole('status');
  }

  // Per-env controls have no per-env label; index follows configured env order.
  reviewBoundaryButton(label: string, index: number): Locator {
    return this.page.getByRole('button', { name: new RegExp(label) }).nth(index);
  }

  noLocalChangesNotice(container: Locator): Locator {
    return container.getByRole('status').filter({ hasText: 'No local changes' });
  }

  viewAllBranchChangesButton(container: Locator): Locator {
    return container.getByRole('button', { name: 'View all branch changes' });
  }

  // The panel loads diffs only when it opens.
  async refreshDiff(): Promise<void> {
    await this.page.getByRole('button', { name: 'Refresh diff' }).click();
  }
}
