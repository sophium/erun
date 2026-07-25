import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';

// ParseGitDiff already orders the diff files to match the changed-files tree;
// these specs lock that the desktop panels agree — same files, same order,
// under an active filter and collapsed directories — and that the tree
// auto-scrolls to keep the file in the diff viewport visible.

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

// diff.files is in tree pre-order — the ordering contract the desktop relies on.
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

    // Scrolling the diff drives the scrollspy to a late file; the tree must
    // follow to keep that node visible. The diff section can still re-render as
    // it settles (30 tall files), so the last node may detach between resolving
    // it and scrolling on a loaded host — retry so the locator re-resolves
    // against the current DOM rather than scrolling a stale, detached node.
    await expect(async () => {
      await page.locator('.diff-file[data-path]').last().scrollIntoViewIfNeeded();
    }).toPass();

    const node = review.currentTreeNode();
    await expect(node).toBeVisible();
    // The auto-scroll guarantee: without it the active node would sit below the
    // tree container once the diff scrolls to the bottom.
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
