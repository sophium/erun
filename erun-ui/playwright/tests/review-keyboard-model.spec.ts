import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import {
  removeEnvironment,
  SEED_TENANT,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/index.js';

// The review surface's keyboard model (erun-ui/AGENTS.md § "The keyboard
// model the review surface still owes", issue #1421): next/previous changed
// file, next/previous hunk, and starting a review in the diff panel; reply
// and resolve/unresolve on the focused comment thread in the review detail
// dialog. Every binding here is a regression against that decision if it
// ever silently stops firing, since it was reachable no other way before
// this spec existed.

function invokeBody(request: Request): { method: string } {
  return JSON.parse(request.postData() ?? '{}') as { method: string };
}

async function fulfillJSON(route: Route, data: unknown): Promise<void> {
  await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
}

interface DiffLineStub {
  kind: string;
  oldLine: number | null;
  newLine: number | null;
  content: string;
}
function diffLine(newLine: number, content: string): DiffLineStub {
  return { kind: 'add', oldLine: null, newLine, content };
}

// TWO_FILE_DIFF gives the flat hunk order [a.go#1, a.go#2, b.go#1]: two
// hunks in one file exercise next/previous hunk crossing a file boundary,
// and a second file exercises next/previous changed file jumping straight
// past a.go's second hunk.
const TWO_FILE_DIFF = {
  rawDiff: '',
  workingDirectory: '/seed',
  summary: { fileCount: 2, additions: 3, deletions: 0 },
  files: [
    {
      path: 'a.go',
      status: 'modified',
      additions: 2,
      deletions: 0,
      binary: false,
      hunks: [
        { header: '@@ -1,1 +1,1 @@', lines: [diffLine(1, 'package a')] },
        { header: '@@ -5,1 +5,1 @@', lines: [diffLine(5, 'func A() {}')] },
      ],
    },
    {
      path: 'b.go',
      status: 'modified',
      additions: 1,
      deletions: 0,
      binary: false,
      hunks: [{ header: '@@ -1,1 +1,1 @@', lines: [diffLine(1, 'package b')] }],
    },
  ],
  tree: [
    { name: 'a.go', path: 'a.go', type: 'file', depth: 0 },
    { name: 'b.go', path: 'b.go', type: 'file', depth: 0 },
  ],
  reviewBase: { branch: 'main', commit: 'abc123', shortCommit: 'abc123' },
  reviewCommits: [],
  scope: 'current',
  includesWorktree: true,
};

// dismissAIOccupancyPromptIfShown mirrors diff-panel-start-review.spec.ts's
// own helper (not imported: specs stay independent of each other's
// internals, see erun-ui/playwright/AGENTS.md). Opening an environment
// auto-spawns its AI tab, which resolves either into an AI tab or -- if the
// environment's activity lease is already held -- this occupancy prompt.
// This spec cares about the diff panel, not the AI tab, so race on whichever
// the spawn actually produces.
async function dismissAIOccupancyPromptIfShown(app: AppShell): Promise<void> {
  const dialog = app.aiOccupancyPromptDialog;
  await Promise.race([
    dialog.waitForOpen().catch(() => undefined),
    app.page
      .getByRole('tab', { name: 'AI', exact: true })
      .waitFor({ state: 'visible' })
      .catch(() => undefined),
  ]);
  if (await dialog.locator().isVisible()) {
    await dialog.cancel();
    await dialog.waitForClosed();
  }
}

async function openDiffPanel(app: AppShell, tenant: string, environment: string): Promise<void> {
  await app.sidebar.openEnvironment(tenant, environment);
  await dismissAIOccupancyPromptIfShown(app);
  await app.titlebar.toggleReviewPanel();
}

test.describe('diff panel keyboard model — next/previous hunk and changed file (#1421)', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, TWO_FILE_DIFF);
        return;
      }
      await route.continue();
    });
  });

  test('ArrowDown/ArrowUp move focus one hunk at a time, crossing into the next file', async ({
    app,
    page,
    seededEnv,
  }) => {
    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths()).toEqual(['a.go', 'b.go']);

    const aHunk1 = review.hunkRegionAt('a.go', '@@ -1,1 +1,1 @@');
    const aHunk2 = review.hunkRegionAt('a.go', '@@ -5,1 +5,1 @@');
    const bHunk1 = review.hunkRegionAt('b.go', '@@ -1,1 +1,1 @@');

    await aHunk1.focus();
    await expect(aHunk1).toBeFocused();

    await page.keyboard.press('ArrowDown');
    await expect(aHunk2).toBeFocused();
    // Moving focus by keyboard must leave a visible trail, not just a
    // logical one -- the target hunk is scrolled into view as it is focused.
    await expect(aHunk2).toBeInViewport();

    await page.keyboard.press('ArrowDown');
    await expect(bHunk1).toBeFocused();
    await expect(bHunk1).toBeInViewport();

    await page.keyboard.press('ArrowUp');
    await expect(aHunk2).toBeFocused();
  });

  test("] and [ jump straight to the next/previous changed file, skipping a.go's second hunk", async ({
    app,
    page,
    seededEnv,
  }) => {
    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths()).toEqual(['a.go', 'b.go']);

    const aHunk1 = review.hunkRegionAt('a.go', '@@ -1,1 +1,1 @@');
    const bHunk1 = review.hunkRegionAt('b.go', '@@ -1,1 +1,1 @@');

    await aHunk1.focus();
    await page.keyboard.press(']');
    await expect(bHunk1).toBeFocused();
    await expect(bHunk1).toBeInViewport();

    await page.keyboard.press('[');
    await expect(aHunk1).toBeFocused();
  });

  test('the diff panel keyboard model is discoverable from a keyboard-shortcuts popover', async ({
    app,
    seededEnv,
  }) => {
    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths()).toEqual(['a.go', 'b.go']);

    await review.keyboardShortcutsButton().click();
    const popover = review.keyboardShortcutsPopover();
    await expect(popover.getByText('Next / previous hunk')).toBeVisible();
    await expect(popover.getByText('Next / previous changed file')).toBeVisible();
    await expect(popover.getByText('Start a review')).toBeVisible();
  });
});

test.describe('diff panel keyboard model — starting a review with S (#1421)', () => {
  const ONE_FILE_DIFF = {
    ...TWO_FILE_DIFF,
    files: [TWO_FILE_DIFF.files[0]],
    tree: [TWO_FILE_DIFF.tree[0]],
    summary: { fileCount: 1, additions: 2, deletions: 0 },
  };

  test('pressing S opens "Open a review" for the focused environment section', async ({
    app,
    page,
    seededEnv,
  }) => {
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, ONE_FILE_DIFF);
        return;
      }
      if (body.method === 'TenantReviewCreateCapability') {
        await fulfillJSON(route, { canCreate: true });
        return;
      }
      if (body.method === 'EnvironmentWorkingIssue') {
        await fulfillJSON(route, { available: true, branch: 'feature/1421-keyboard' });
        return;
      }
      await route.continue();
    });

    await openDiffPanel(app, seededEnv.tenant, seededEnv.environment);
    const review = app.reviewPanel;
    await expect.poll(() => review.diffSectionPaths()).toEqual(['a.go']);

    await review.hunkRegionAt('a.go', '@@ -1,1 +1,1 @@').focus();
    await expect(app.createReviewDialog.locator()).toBeHidden();

    await page.keyboard.press('s');

    await app.createReviewDialog.waitForOpen();
    await expect(app.createReviewDialog.locator()).toContainText(
      `${seededEnv.tenant} / ${seededEnv.environment}`,
    );
  });
});

// --- Review detail dialog: reply and resolve/unresolve the focused thread ---

const REVIEW = {
  reviewId: 'review-1421',
  tenantId: 't1',
  name: '1421 keyboard review',
  targetBranch: 'main',
  sourceBranch: 'feature/1421-keyboard',
  status: 'READY',
  updatedAt: '2026-01-01T00:00:00Z',
};

interface CommentStub {
  commentId: string;
  creatorUserId: string;
  creatorUsername: string;
  status: string;
  parentCommentId?: string;
  commitId: string;
  filePath: string;
  line: number;
  body: string;
  createdAt: string;
}

function commentStub(
  commentId: string,
  author: string,
  status: string,
  parentCommentId?: string,
): CommentStub {
  return {
    commentId,
    creatorUserId: `u-${author}`,
    creatorUsername: author,
    status,
    parentCommentId,
    commitId: 'abc123',
    filePath: 'a.go',
    line: 1,
    body: `${author}'s comment`,
    createdAt: '2026-01-01T00:00:00Z',
  };
}

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

async function openReviewDetailFromDashboard(
  app: AppShell,
  environment: string,
  reviewName: string,
): Promise<void> {
  await app.reloadEnvironments();
  await app.sidebar
    .envRowButton(SEED_TENANT, environment)
    .waitFor({ state: 'visible', timeout: 15_000 });
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.tenantDashboard.waitForOpen();
  await app.tenantDashboard.selectTab('Reviews');
  await app.tenantDashboard.openReview(reviewName);
  await app.reviewDetailDialog.waitForOpen();
}

test.describe('review detail dialog keyboard model — reply and resolve/unresolve (#1421)', () => {
  test('Up/Down move focus between threads, R replies (typing still works), Enter resolves', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('review-keyboard-reply');
    try {
      let resolved = false;
      let replySubmittedBody = '';
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            platformAlias: 'pw-aws',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [REVIEW],
            mergeQueue: [],
            panels: [{ tab: 'reviews' }],
            canCreateReview: false,
            canAdvanceMergeQueue: false,
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          const comments = [
            commentStub('c-alice', 'alice', resolved ? 'CLOSED' : 'OPEN'),
            commentStub('c-bob', 'bob', 'OPEN'),
          ];
          if (replySubmittedBody) {
            comments.push({
              ...commentStub('c-alice-reply', 'operator', 'OPEN', 'c-alice'),
              body: replySubmittedBody,
            });
          }
          await fulfillJSON(route, {
            reviewId: REVIEW.reviewId,
            review: REVIEW,
            canComment: true,
            canClose: false,
            canResolveComments: true,
            comments,
            unresolvedThreads: resolved ? 1 : 2,
          });
          return;
        }
        if (body.method === 'CreateReviewReply') {
          const parsed = JSON.parse(request.postData() ?? '{}') as {
            args: [{ body: string }];
          };
          replySubmittedBody = parsed.args[0].body;
          await fulfillJSON(route, commentStub('c-alice-reply', 'operator', 'OPEN', 'c-alice'));
          return;
        }
        if (body.method === 'ResolveReviewComment') {
          resolved = true;
          await fulfillJSON(route, commentStub('c-alice', 'alice', 'CLOSED'));
          return;
        }
        await route.continue();
      });

      await openReviewDetailFromDashboard(app, environment, REVIEW.name);
      const dialog = app.reviewDetailDialog;
      const aliceThread = dialog.commentThread('alice');
      const bobThread = dialog.commentThread('bob');
      await expect(aliceThread).toBeVisible();
      await expect(bobThread).toBeVisible();

      await aliceThread.focus();
      await expect(aliceThread).toBeFocused();

      await page.keyboard.press('ArrowDown');
      await expect(bobThread).toBeFocused();
      await page.keyboard.press('ArrowUp');
      await expect(aliceThread).toBeFocused();

      // R opens the reply composer for the focused thread (alice's).
      await page.keyboard.press('r');
      const replyInput = dialog.replyInput();
      await expect(replyInput).toBeFocused();

      // Typing in the composer must still type -- this is the regression
      // most likely to be introduced by a new binding and least likely to
      // be noticed. The typed text itself contains 'r' and is followed by a
      // real Enter press, exercising both bindings' guard at once.
      await page.keyboard.type('has an r in it');
      await expect(replyInput).toHaveValue('has an r in it');
      await page.keyboard.press('Enter');
      await expect.poll(() => replySubmittedBody).toBe('has an r in it');
      await expect(replyInput).toBeHidden();

      // Submitting invalidates and reloads the whole review detail
      // (loadReviewDetail), which flashes the dialog to its loading state
      // and remounts the comments list once the reload lands -- true for a
      // mouse click on Send today too, not something this keyboard model
      // introduces. The remounted list resets to its default roving focus
      // (the first thread); re-focus it the way one more Tab press would.
      await expect(aliceThread).toBeVisible();
      await aliceThread.focus();
      await expect(aliceThread).toBeFocused();

      // Enter resolves the focused (alice's) thread -- not a reply target.
      await expect(aliceThread.getByText('Unresolved')).toBeVisible();
      await page.keyboard.press('Enter');
      await expect(aliceThread.getByText('Resolved')).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('the review detail dialog keyboard model is discoverable from a keyboard-shortcuts popover', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('review-keyboard-hint');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            platformAlias: 'pw-aws',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [REVIEW],
            mergeQueue: [],
            panels: [{ tab: 'reviews' }],
            canCreateReview: false,
            canAdvanceMergeQueue: false,
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            reviewId: REVIEW.reviewId,
            review: REVIEW,
            canComment: true,
            canClose: false,
            canResolveComments: true,
            comments: [commentStub('c-alice', 'alice', 'OPEN')],
            unresolvedThreads: 1,
          });
          return;
        }
        await route.continue();
      });

      await openReviewDetailFromDashboard(app, environment, REVIEW.name);
      await app.reviewDetailDialog.keyboardShortcutsButton().click();
      await expect(app.page.getByText('Reply to the focused thread')).toBeVisible();
      await expect(app.page.getByText('Resolve / reopen the focused thread')).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
