import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

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

// The panel reloads its diff on a timer. A spec that drives panel state wants the
// quiet window just after one of those reloads, not whatever moment a re-render
// happens to land on.
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

// scrollDiffToLastFile scrolls the diff panel to its last file without going
// through Playwright's actionability gate. scrollIntoViewIfNeeded additionally
// waits for the element to be stable — the same box across two consecutive
// animation frames — which a starved page cannot promise inside any per-attempt
// bound (see the scroll spec's own comment for the measurement). Waiting for
// the element to be attached is what this step actually needs; the retry that
// re-resolves a detached node is still the caller's toPass, and the bounded
// waitFor keeps that attempt from consuming the whole retry budget.
async function scrollDiffToLastFile(page: Page): Promise<void> {
  const last = page.locator('.diff-file[data-path]').last();
  await last.waitFor({ state: 'attached', timeout: 2_000 });
  await last.evaluate((el) => {
    el.scrollIntoView({ block: 'nearest' });
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
    // Converge on the panel having opened before asserting on the tree or the
    // diffs it renders.
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect(review.changedFilesTree()).toBeVisible();
    await expect.poll(() => review.diffSectionPaths()).toEqual(SMALL_ORDER);
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
    // Converge on the panel having opened before asserting on the tree or the
    // diffs it renders.
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths()).toEqual(SMALL_ORDER);

    // The filter field is a controlled input fed from the store, and the panel
    // re-renders on its own silent diff refresh, so a fill landing inside that
    // window is rewritten from the pre-change value — the intermittent red this
    // spec used to produce. Anchoring the fill to a completed refresh leaves a
    // whole refresh interval of quiet, so what follows waits on observable state
    // instead of racing the timer, and a filter that never applies fails loudly
    // on its own value rather than being retried until it sticks.
    await nextDiffRefresh(page);
    await review.setDiffFilter('b.ts');
    await expect(review.filterInput()).toHaveValue('b.ts');
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
    // Converge on the panel having opened before asserting on the tree or the
    // diffs it renders.
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths()).toEqual(SMALL_ORDER);

    await review.collapseDirectory('src');
    await expect.poll(() => review.treeFilePaths()).toEqual(['docs/c.md']);
    await expect.poll(() => review.diffSectionPaths()).toEqual(['docs/c.md']);
  });

  test('the tree scrolls to keep the active file visible as the diff scrolls', async ({
    app,
    page,
    seededEnv,
  }) => {
    // This spec renders 30 tall files -- far more than the default single-file
    // case the suite's global 30s per-test timeout is tuned for -- and then
    // runs two sequential convergence waits on top of that render (the scroll
    // retry and the tree auto-scroll poll below), so the legitimate cost of
    // this spec alone can approach the default budget before either wait
    // starts (root AGENTS.md's "no flaky tests" gate needs this to be a real
    // budget increase, not a race against the whole-test clock the bounded
    // retries below would still lose).
    //
    // The two bounds are related, not independent: the auto-scroll poll below
    // must not be the thing that expires first. It carried a fixed 40s while
    // the test's own budget was 120s, so under a contended builder the poll
    // gave up 80s before the clock the scenario was actually sized against --
    // observed as this spec failing on the full suite's own load while its
    // assertions were still being satisfied. The poll is widened to sit inside
    // this budget, and the budget itself raised because the gate's concurrency
    // is far past the "approach the default" case the 120s was chosen for.
    test.setTimeout(240_000);
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
    // Converge on the panel having opened before asserting on the tree or the
    // diffs it renders.
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths().then((paths) => paths.length)).toBe(30);

    // The panel keeps reloading its diff on a timer (see nextDiffRefresh's own
    // comment above); anchor to the quiet window right after one of those
    // reloads before driving the scroll below, the same reasoning the filter
    // test already applies to its own fill.
    await nextDiffRefresh(page);

    // Scrolling the diff drives the scrollspy to a late file; the tree must
    // follow to keep that node visible. The diff section can still re-render as
    // it settles (30 tall files), so the last node may detach between resolving
    // it and scrolling on a loaded host — retry so the locator re-resolves
    // against the current DOM rather than scrolling a stale, detached node.
    //
    // The scroll itself is scrollDiffToLastFile, not scrollIntoViewIfNeeded.
    // Scrolling is not an action that needs Playwright's actionability gate:
    // that gate additionally requires the element's box to be unchanged across
    // two consecutive animation frames, and this page is expensive enough that
    // a loaded host can be a second or more from its next frame -- measured on
    // the contended venue, 5-15 frames per 4s with gaps up to 1.4s, while the
    // diff section's own box never moved. The element resolved and was visible
    // and the call still timed out every attempt, for the whole retry budget.
    // Attached is the one property this step depends on, and toPass still
    // supplies the re-resolve.
    await expect(async () => {
      await scrollDiffToLastFile(page);
    }).toPass({ timeout: 30_000 });

    const node = review.currentTreeNode();
    await expect(node).toBeVisible();
    // The auto-scroll guarantee: without it the active node would sit below the
    // tree container once the diff scrolls to the bottom. boundingBox() also
    // has no timeout of its own, so the same reasoning applies: bound each
    // read so a node that is momentarily missing its aria-current (mid
    // scrollspy re-render) fails fast and the poll gets another attempt,
    // rather than one attempt consuming the whole convergence window.
    await expect
      .poll(
        async () => {
          const nb = await node.boundingBox({ timeout: 2_000 }).catch(() => null);
          const cb = await review
            .changedFilesTree()
            .boundingBox({ timeout: 2_000 })
            .catch(() => null);
          if (!nb || !cb) {
            return false;
          }
          return nb.y >= cb.y - 2 && nb.y + nb.height <= cb.y + cb.height + 2;
        },
        { timeout: 120_000 },
      )
      .toBe(true);
  });

  // The scroll spec above converges on one predicate: the tree's active node
  // has a box inside the tree container's box. That predicate can only ever
  // become true while the tree is still marking a file `aria-current`, and the
  // panel reloads its diff on a 5s timer (nextDiffRefresh's own comment). This
  // spec pins the invariant that makes the predicate reachable at all: the
  // active file the diff scrollspy selected must survive one of those reloads.
  //
  // It is the deterministic rendering of the contended red the scroll spec
  // produces, which is otherwise a coin flip on whether a predicate evaluation
  // lands before or after the next reload: the reload used to clear the tree's
  // active node outright and nothing ever put it back, because the only writer
  // that could was the scrollspy, and the diff is no longer scrolling. A run
  // that loses that race then reports "Timeout 120000ms exceeded while waiting
  // on the predicate" -- not a slow convergence, a state that never exists
  // again.
  test('the tree keeps the file the diff scrolled to active across a diff reload', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(120_000);
    // One line per file: enough sections that the diff panel scrolls, without
    // the scroll spec's 18-line files. This spec asserts the selection's
    // lifetime, not the render's, and the panel re-renders the whole list on
    // every periodic reload -- carrying the sibling spec's 540 rendered lines
    // here too just adds load to the sub-suite both specs share.
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
    await expect.poll(() => review.diffSectionPaths().then((paths) => paths.length)).toBe(30);

    await nextDiffRefresh(page);
    await expect(async () => {
      await scrollDiffToLastFile(page);
    }).toPass({ timeout: 30_000 });

    // What the scrollspy picked, read off the tree rather than assumed: which
    // file spans the diff viewport's anchor depends on the rendered heights.
    const node = review.currentTreeNode();
    await expect(node).toBeVisible();
    // evaluate, not getAttribute: this one has to be carried forward as a
    // value to compare against after the reload, which is a read rather than
    // an assertion about the node as it stands now.
    const active = await node.evaluate((el) => el.getAttribute('data-path'));
    expect(active).toBeTruthy();

    // One periodic reload lands. Nothing scrolls the diff after this, so the
    // scrollspy does not run again -- whatever the reload leaves the tree
    // marking is the last word.
    await nextDiffRefresh(page);

    await expect(review.currentTreeNode()).toHaveAttribute('data-path', active ?? '');

    // And the guarantee that node being current encodes still holds -- the
    // same predicate the scroll spec asserts, read the same bounded way so a
    // reload landing mid-measurement costs one retry rather than the read.
    await expect
      .poll(
        async () => {
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
        },
        { timeout: 20_000 },
      )
      .toBe(true);
  });

  // Regression coverage for the diff panel's "current" scope reading as a
  // flat "No changes" when the environment actually has commits ahead of the
  // review base (e.g. just-committed, unpushed work) -- DiffEmptyState must
  // point at "All branch changes" instead of asserting nothing is there to
  // review. Covers both surfaces DiffEmptyState renders into (the diff panel
  // body and the changed-files tree aside), and that the pointer's own button
  // actually switches scope and loads the commits it named.
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
    // Converge on the panel having opened before asserting on the tree or the
    // diffs it renders.
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;

    const panelNotice = review.noLocalChangesNotice(review.diffContentRegion());
    const treeNotice = review.noLocalChangesNotice(review.changedFilesTree());
    await expect(panelNotice).toBeVisible();
    await expect(panelNotice).toContainText('2 commits');
    await expect(treeNotice).toBeVisible();
    // Never a flat "No changes" that hides the two pending commits.
    await expect(review.diffContentRegion().getByText('No changes', { exact: true })).toHaveCount(
      0,
    );

    await review.viewAllBranchChangesButton(review.diffContentRegion()).click();
    await expect.poll(() => review.diffSectionPaths()).toEqual(['feature.txt']);
    await expect(panelNotice).toHaveCount(0);
  });
});
