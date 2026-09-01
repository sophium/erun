import type { Locator, Page } from '@playwright/test';

// ReviewPanel is the page object for the right-hand review panel.
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

  // The scrollable diff content region (ReviewPanel.tsx); focusable so a
  // keyboard-only reviewer can scroll past the first screenful of a diff.
  diffContentRegion(): Locator {
    return this.page.getByRole('region', { name: 'Diff content' });
  }

  // One horizontal scroller per hunk (DiffList.tsx), focusable so a keyboard
  // user can read a line wider than the panel. `filePath` narrows to one
  // file's hunk(s) when more than one diff section is rendered.
  hunkRegion(filePath: string): Locator {
    return this.page.getByRole('region', { name: new RegExp(`^Diff for ${filePath} `) });
  }

  // hunkRegionAt narrows hunkRegion() to one specific hunk of a file that has
  // more than one, matching its header text too -- needed for the keyboard
  // navigation specs, which move between a file's own hunks one at a time.
  hunkRegionAt(filePath: string, header: string): Locator {
    return this.page.getByRole('region', {
      name: `Diff for ${filePath} at ${header}`,
      exact: true,
    });
  }

  // The "Keyboard shortcuts" popover DiffList.tsx renders per environment
  // section (ReviewKeyboardShortcuts.tsx) -- the review surface's
  // discoverability affordance for its keyboard model.
  keyboardShortcutsButton(): Locator {
    return this.page.getByRole('button', { name: 'Keyboard shortcuts' });
  }

  // Scoped to the popover's own content, not the whole page: the diff panel
  // already renders "Start a review" as a real button label, which a
  // page-wide text match would also resolve to.
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

  // changedFilesEnvLabel locates the changed-files tree's own copy of the
  // shared ReviewEnvLabel (#1314) — scoped to the tree container so it can't
  // also match the diff panel's or the review-layers block's copy of the same
  // "tenant / environment" text.
  changedFilesEnvLabel(envKey: string): Locator {
    const [tenant, environment] = envKey.split('/');
    return this.changedFilesTree().getByText(`${tenant} / ${environment}`, { exact: true });
  }

  // envLabels locates every rendered copy of the shared ReviewEnvLabel for
  // one environment across the whole review surface (review-layers block,
  // changed-files tree, and diff panel section header) — three when more
  // than one environment is in scope, proving all three share one treatment.
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

  // envSectionHeader locates the sticky per-environment header the diff panel
  // renders above each linked environment's section once more than one target
  // is shown (#1178). Scoped to DiffList's sticky header class combination
  // (unique in the frontend source) rather than a plain text match: the
  // changed-files tree renders its own per-env header with the same label
  // text (#1314 made the two share one ReviewEnvLabel treatment), and other
  // surfaces (sidebar rows, tab labels) can also contain it. envKey is the
  // internal `tenant/environment` form; the rendered label spaces the slash
  // ("tenant / environment"), so this reformats before matching.
  envSectionHeader(envKey: string): Locator {
    const [tenant, environment] = envKey.split('/');
    return this.page
      .locator('.sticky.top-0.z-10.border-b')
      .filter({ hasText: `${tenant} / ${environment}` });
  }

  // envActionHeader locates the sticky per-environment header by its
  // data-env-key attribute (unlike envSectionHeader, which matches by visible
  // tenant/environment text and so only resolves in the multi-env case).
  // Scoped with `.sticky` since `data-env-key` also appears on each of that
  // environment's own diff-file sections (DiffFileView), which are not sticky.
  envActionHeader(envKey: string): Locator {
    return this.page.locator(`[data-env-key="${envKey}"].sticky`);
  }

  // reviewStatusChip locates the review-status chip's own label text
  // (DiffList.ReviewAction.tsx's DiffReviewStatusChip), scoped to one
  // environment section and matched exactly -- a substring match risks
  // colliding with a longer label sharing the same word (e.g. "Ready" inside
  // "Already").
  reviewStatusChip(envKey: string, label: string): Locator {
    return this.envActionHeader(envKey).getByText(label, { exact: true });
  }

  // reviewActionButton locates the diff panel's one action button for an
  // environment section (DiffReviewAction), by its current accessible name --
  // "Start a review", "Advance queue", "Resolve N threads", "View review", …
  reviewActionButton(envKey: string, name: string | RegExp): Locator {
    return this.envActionHeader(envKey).getByRole('button', { name });
  }

  // The "Changed files N" collapsible header inside the aside, distinct from
  // the titlebar's "Toggle changed files list" (which hides the whole aside).
  changedFilesSectionToggle(): Locator {
    return this.page.getByRole('button', { name: /^Changed files/ });
  }

  async collapseChangedFilesSection(): Promise<void> {
    await this.changedFilesSectionToggle().click();
  }

  // The changed-files tree renders a short status line rather than its own
  // copy of the diff panel's alert for the same per-env slot (#1230) — the
  // diff panel is the one place a linked environment's outage renders as an
  // actionable alert, so this locator only ever matches once per environment.
  errorAlerts(): Locator {
    return this.page.getByRole('alert');
  }

  // reachabilityStatuses locates the informational "environment not running"
  // status DiffErrorAlert renders for the not-open reachability kind (#1230).
  // Unlike errorAlerts() this is role="status", not role="alert": it is not a
  // fault, so it must not be announced as one (WCAG 4.1.3 / Nielsen #1).
  reachabilityStatuses(): Locator {
    return this.page.getByRole('status');
  }

  // reviewBoundaryButton locates a "Review layers" scope button by its visible
  // label (e.g. "Current local changes", "All branch changes"). Each linked
  // environment renders its own control with no per-env accessible label, so
  // callers scope by DOM order, which matches the environments' configured
  // order (selectReviewEnvTargets).
  reviewBoundaryButton(label: string, index: number): Locator {
    return this.page.getByRole('button', { name: new RegExp(label) }).nth(index);
  }

  // noLocalChangesNotice locates DiffEmptyState's "no local changes, but
  // commits are pending" notice (DiffList.tsx) within a given container --
  // the same component renders in both the diff panel body and the
  // changed-files tree aside, so callers scope to diffContentRegion() or
  // changedFilesTree() to assert each independently.
  noLocalChangesNotice(container: Locator): Locator {
    return container.getByRole('status').filter({ hasText: 'No local changes' });
  }

  viewAllBranchChangesButton(container: Locator): Locator {
    return container.getByRole('button', { name: 'View all branch changes' });
  }
}
