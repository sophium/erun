import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

// The diff panel and changed-files tree must agree on files and order, and the
// tree must auto-scroll to keep the visible diff file in view.

interface DiffLineStub {
  kind: string;
  oldLine: number | null;
  newLine: number | null;
  content: string;
}
interface DiffFileStub {
  path: string;
  status: string;
  additions: number;
  deletions: number;
  binary: boolean;
  hunks: { header: string; lines: DiffLineStub[] }[];
}
interface DiffNodeStub {
  name: string;
  path: string;
  parentPath?: string;
  type: 'file' | 'directory';
  depth: number;
}

function dirNode(path: string, name: string, depth: number): DiffNodeStub {
  return { name, path, type: 'directory', depth };
}
function fileNode(path: string, name: string, parentPath: string, depth: number): DiffNodeStub {
  return { name, path, parentPath, type: 'file', depth };
}
function diffFile(path: string, lines: number): DiffFileStub {
  return {
    path,
    status: 'modified',
    additions: lines,
    deletions: 0,
    binary: false,
    hunks: [
      {
        header: `@@ -1,${String(lines)} +1,${String(lines)} @@`,
        lines: Array.from({ length: lines }, (_, i) => ({
          kind: 'add',
          oldLine: null,
          newLine: i + 1,
          content: `${path}:${String(i)}`,
        })),
      },
    ],
  };
}
function diffResult(files: DiffFileStub[], tree: DiffNodeStub[]): unknown {
  return {
    rawDiff: '',
    workingDirectory: '/seed',
    summary: {
      fileCount: files.length,
      additions: files.reduce((sum, f) => sum + f.additions, 0),
      deletions: 0,
    },
    files,
    tree,
    scope: 'current',
    includesWorktree: true,
  };
}

async function stubDiff(page: Page, diff: unknown): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadDiff') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: diff }),
      });
    }
    await route.continue();
  });
}

// Resolves just after a periodic diff reload, at the start of a quiet window.
async function nextDiffRefresh(page: Page): Promise<void> {
  await page.waitForResponse((res) => {
    if (!res.url().includes('/__erun_invoke')) return false;
    try {
      const body = JSON.parse(res.request().postData() ?? '{}') as { method?: string };
      return body.method === 'LoadDiff';
    } catch {
      return false;
    }
  });
}

// Avoids scrollIntoViewIfNeeded's stability gate, which a starved page cannot
// satisfy; only attachment is needed, and the caller's toPass re-resolves.
async function scrollDiffToLastFile(page: Page): Promise<void> {
  const last = page.locator('.diff-file[data-path]').last();
  await last.waitFor({ state: 'attached', timeout: 2_000 });
  await last.evaluate((el) => {
    el.scrollIntoView({ block: 'nearest' });
  });
}

// diff.files is in tree pre-order.
const SMALL_FILES = [diffFile('src/a.ts', 2), diffFile('src/b.ts', 2), diffFile('docs/c.md', 2)];
const SMALL_TREE = [
  dirNode('src', 'src', 0),
  fileNode('src/a.ts', 'a.ts', 'src', 1),
  fileNode('src/b.ts', 'b.ts', 'src', 1),
  dirNode('docs', 'docs', 0),
  fileNode('docs/c.md', 'c.md', 'docs', 1),
];
const SMALL_ORDER = ['src/a.ts', 'src/b.ts', 'docs/c.md'];

test.describe('review diff/tree consistency', () => {
  test('the diff panel renders files in the changed-files tree order', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubDiff(page, diffResult(SMALL_FILES, SMALL_TREE));
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect(review.changedFilesTree()).toBeVisible(withTestBudget());
    await expect.poll(() => review.diffSectionPaths(), withTestBudget()).toEqual(SMALL_ORDER);
    expect(await review.treeFilePaths()).toEqual(SMALL_ORDER);
  });

  test('an active filter narrows the diff panel and tree to the same files', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubDiff(page, diffResult(SMALL_FILES, SMALL_TREE));
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths(), withTestBudget()).toEqual(SMALL_ORDER);

    // The controlled filter input is rewritten if a refresh lands mid-fill, so
    // anchor the fill to a completed refresh.
    await nextDiffRefresh(page);
    await review.setDiffFilter('b.ts');
    await expect(review.filterInput()).toHaveValue('b.ts');
    await expect.poll(() => review.treeFilePaths(), withTestBudget()).toEqual(['src/b.ts']);
    await expect.poll(() => review.diffSectionPaths(), withTestBudget()).toEqual(['src/b.ts']);
  });

  test('collapsing a directory hides its files in both the tree and the diff', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubDiff(page, diffResult(SMALL_FILES, SMALL_TREE));
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths(), withTestBudget()).toEqual(SMALL_ORDER);

    await review.collapseDirectory('src');
    await expect.poll(() => review.treeFilePaths(), withTestBudget()).toEqual(['docs/c.md']);
    await expect.poll(() => review.diffSectionPaths(), withTestBudget()).toEqual(['docs/c.md']);
  });

  test('the tree scrolls to keep the active file visible as the diff scrolls', async ({
    app,
    page,
    seededEnv,
  }) => {
    // 30 tall files plus two sequential convergence waits; the poll below must
    // expire inside this budget, not before it.
    test.setTimeout(240_000);
    const big = Array.from({ length: 30 }, (_, i) => `pkg/f${String(i).padStart(2, '0')}.ts`);
    const files = big.map((path) => diffFile(path, 18));
    const tree = [
      dirNode('pkg', 'pkg', 0),
      ...big.map((path) => fileNode(path, path.slice('pkg/'.length), 'pkg', 1)),
    ];
    await stubDiff(page, diffResult(files, tree));
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect
      .poll(() => review.diffSectionPaths().then((paths) => paths.length), withTestBudget())
      .toBe(30);

    await nextDiffRefresh(page);

    // The diff can re-render as it settles, so retry to re-resolve a detached node.
    await expect(async () => {
      await scrollDiffToLastFile(page);
    }).toPass();

    const node = review.currentTreeNode();
    await expect(node).toBeVisible();
    // boundingBox() has no timeout, so each read is bounded to let the poll retry.
    await expect
      .poll(async () => {
        const nb = await node.boundingBox({ timeout: 2_000 }).catch(() => null);
        const cb = await review
          .changedFilesTree()
          .boundingBox({ timeout: 2_000 })
          .catch(() => null);
        if (!nb || !cb) {
          return false;
        }
        return nb.y >= cb.y - 2 && nb.y + nb.height <= cb.y + cb.height + 2;
      }, withTestBudget())
      .toBe(true);
  });

  // A periodic reload must keep the tree's active file, or the scroll predicate
  // above can never become true again once the diff stops scrolling.
  test('the tree keeps the file the diff scrolled to active across a diff reload', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(120_000);
    // One-line files: this asserts the selection's lifetime, not rendering.
    const big = Array.from({ length: 30 }, (_, i) => `pkg/f${String(i).padStart(2, '0')}.ts`);
    const files = big.map((path) => diffFile(path, 2));
    const tree = [
      dirNode('pkg', 'pkg', 0),
      ...big.map((path) => fileNode(path, path.slice('pkg/'.length), 'pkg', 1)),
    ];
    await stubDiff(page, diffResult(files, tree));
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect
      .poll(() => review.diffSectionPaths().then((paths) => paths.length), withTestBudget())
      .toBe(30);

    await nextDiffRefresh(page);
    await expect(async () => {
      await scrollDiffToLastFile(page);
    }).toPass();

    // The scrollspy update is deferred to the next animation frame, so wait for it
    // to settle before reading the active file.
    await expect.poll(() => review.diffScrollSelectionSettled(), withTestBudget()).toBe(true);

    // Which file spans the viewport anchor depends on rendered heights.
    const node = review.currentTreeNode();
    await expect(node).toBeVisible();
    const active = await node.evaluate((el) => el.getAttribute('data-path'));
    expect(active).toBeTruthy();

    // Nothing scrolls after this, so the scrollspy does not run again.
    await nextDiffRefresh(page);

    await expect(review.currentTreeNode()).toHaveAttribute('data-path', active ?? '');

    await expect
      .poll(async () => {
        const nb = await review
          .currentTreeNode()
          .boundingBox({ timeout: 2_000 })
          .catch(() => null);
        const cb = await review
          .changedFilesTree()
          .boundingBox({ timeout: 2_000 })
          .catch(() => null);
        if (!nb || !cb) {
          return false;
        }
        return nb.y >= cb.y - 2 && nb.y + nb.height <= cb.y + cb.height + 2;
      }, withTestBudget())
      .toBe(true);
  });

  // With commits ahead of base, an empty "current" scope must point at "All
  // branch changes" rather than read as "No changes".
  test('an empty "current" scope with commits ahead of base points at "All branch changes" in both the tree and the diff panel', async ({
    app,
    page,
    seededEnv,
  }) => {
    const emptyWithCommits = {
      rawDiff: '',
      workingDirectory: '/seed',
      summary: { fileCount: 0, additions: 0, deletions: 0 },
      files: [],
      tree: [],
      reviewBase: { branch: 'main', commit: 'def456', shortCommit: 'def456' },
      reviewCommits: [
        {
          hash: 'a1',
          shortHash: 'a1',
          subject: 'Add widget',
          author: 'Test',
          date: '2026-01-01T00:00:00Z',
        },
        {
          hash: 'a2',
          shortHash: 'a2',
          subject: 'Fix widget',
          author: 'Test',
          date: '2026-01-02T00:00:00Z',
        },
      ],
      scope: 'current',
      includesWorktree: true,
    };
    const allBranchChanges = diffResult(
      [diffFile('feature.txt', 2)],
      [fileNode('feature.txt', 'feature.txt', '', 0)],
    );
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as {
        method: string;
        args?: [unknown, { scope?: string }?];
      };
      if (body.method === 'LoadDiff') {
        const requestedScope = body.args?.[1]?.scope;
        const data = requestedScope === 'all' ? allBranchChanges : emptyWithCommits;
        return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
      }
      await route.continue();
    });
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;

    const panelNotice = review.noLocalChangesNotice(review.diffContentRegion());
    const treeNotice = review.noLocalChangesNotice(review.changedFilesTree());
    await expect(panelNotice).toBeVisible(withTestBudget());
    await expect(panelNotice).toContainText('2 commits', withTestBudget());
    await expect(treeNotice).toBeVisible(withTestBudget());
    await expect(review.diffContentRegion().getByText('No changes', { exact: true })).toHaveCount(
      0,
    );

    await review.viewAllBranchChangesButton(review.diffContentRegion()).click();
    await expect.poll(() => review.diffSectionPaths(), withTestBudget()).toEqual(['feature.txt']);
    await expect(panelNotice).toHaveCount(0);
  });

  // LoadDiff is held just past expect's 10s default.
  test('a diff read that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    let held = false;
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadDiff') {
        if (!held) {
          held = true;
          await new Promise((resolve) => setTimeout(resolve, 12_000));
        }
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: diffResult(SMALL_FILES, SMALL_TREE) }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    await expect
      .poll(() => app.reviewPanel.diffSectionPaths(), withTestBudget())
      .toEqual(SMALL_ORDER);
  });
});
