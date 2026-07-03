import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';

// Two related review-panel guarantees.
//   Part 2 — the diff panel renders the same files as the changed-files tree,
//   in the same order, honouring the active filter and collapsed directories.
//   Part 1 — the tree scrolls to keep the file currently in the diff viewport
//   visible as the diff is scrolled.
//
// The diff payload is stubbed via the LoadDiff RPC over /__erun_invoke (the
// same technique sidebar-upgrade-all.spec.ts uses), so no live cluster is
// needed. ParseGitDiff already orders diff.files to match the tree;
// these specs lock that the rendered panels agree.

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

// Small two-directory tree for the order / filter / collapse cases. diff.files
// is in tree pre-order (the contract the desktop relies on).
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
    const review = app.reviewPanel;
    await expect(review.changedFilesTree()).toBeVisible({ timeout: 10_000 });
    await expect.poll(() => review.diffSectionPaths(), { timeout: 10_000 }).toEqual(SMALL_ORDER);
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
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths(), { timeout: 10_000 }).toEqual(SMALL_ORDER);

    await review.setDiffFilter('b.ts');
    await expect.poll(() => review.treeFilePaths()).toEqual(['src/b.ts']);
    await expect.poll(() => review.diffSectionPaths()).toEqual(['src/b.ts']);
  });

  test('collapsing a directory hides its files in both the tree and the diff', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubDiff(page, diffResult(SMALL_FILES, SMALL_TREE));
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths(), { timeout: 10_000 }).toEqual(SMALL_ORDER);

    await review.collapseDirectory('src');
    await expect.poll(() => review.treeFilePaths()).toEqual(['docs/c.md']);
    await expect.poll(() => review.diffSectionPaths()).toEqual(['docs/c.md']);
  });

  test('the tree scrolls to keep the active file visible as the diff scrolls', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Enough tall files that the tree overflows its container and the active
    // node would otherwise scroll out of view.
    const big = Array.from({ length: 30 }, (_, i) => `pkg/f${String(i).padStart(2, '0')}.ts`);
    const files = big.map((path) => diffFile(path, 18));
    const tree = [
      dirNode('pkg', 'pkg', 0),
      ...big.map((path) => fileNode(path, path.slice('pkg/'.length), 'pkg', 1)),
    ];
    await stubDiff(page, diffResult(files, tree));
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;
    await expect
      .poll(() => review.diffSectionPaths().then((paths) => paths.length), { timeout: 10_000 })
      .toBe(30);

    // Scroll the diff to its last section; the scrollspy selects a late file
    // and the tree must scroll to keep that node visible.
    await page.locator('.diff-file[data-path]').last().scrollIntoViewIfNeeded();

    const node = review.currentTreeNode();
    await expect(node).toBeVisible();
    // The active node must lie within the tree container's visible rect — the
    // auto-scroll guarantee. Without it the node would sit below the container
    // after scrolling the diff to the bottom.
    await expect
      .poll(
        async () => {
          const nb = await node.boundingBox();
          const cb = await review.changedFilesTree().boundingBox();
          if (!nb || !cb) {
            return false;
          }
          return nb.y >= cb.y - 2 && nb.y + nb.height <= cb.y + cb.height + 2;
        },
        { timeout: 10_000 },
      )
      .toBe(true);
  });
});
