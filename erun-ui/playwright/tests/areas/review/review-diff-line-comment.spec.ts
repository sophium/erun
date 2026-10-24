import type { Request, Route } from '@playwright/test';

import { expect, test, waitForSeededRow, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  removeEnvironment,
  SEED_TENANT,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// Locator assertions default to expect's 10s, so reads of LoadDiff's answer
// carry withTestBudget().

function invokeBody(request: Request): { method: string } {
  return JSON.parse(request.postData() ?? '{}') as { method: string };
}

async function fulfillJSON(route: Route, data: unknown): Promise<void> {
  await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
}

const DIFF = {
  rawDiff: '',
  workingDirectory: '/seed',
  summary: { fileCount: 1, additions: 1, deletions: 0 },
  files: [
    {
      path: 'main.go',
      status: 'modified',
      additions: 1,
      deletions: 0,
      binary: false,
      hunks: [
        {
          header: '@@ -1,0 +1,1 @@',
          lines: [{ kind: 'add', oldLine: null, newLine: 1, content: 'package main' }],
        },
      ],
    },
  ],
  tree: [{ name: 'main.go', path: 'main.go', type: 'file', depth: 0 }],
  reviewCommits: [
    {
      hash: 'abc123',
      shortHash: 'abc123',
      subject: 'Add main',
      author: 'operator',
      date: '2026-01-01T00:00:00Z',
    },
  ],
  scope: 'all',
  includesWorktree: false,
};

// An extra hunk renders above 'package main', moving that row out from under a
// resting pointer, as a periodic refresh can.
const DIFF_GROWN = {
  ...DIFF,
  files: [
    {
      path: 'main.go',
      status: 'modified',
      additions: 1,
      deletions: 0,
      binary: false,
      hunks: [
        {
          header: '@@ -0,0 +0,0 @@',
          lines: [
            { kind: 'meta', oldLine: null, newLine: null, content: 'index 1111111..2222222' },
          ],
        },
        {
          header: '@@ -1,0 +1,1 @@',
          lines: [{ kind: 'add', oldLine: null, newLine: 1, content: 'package main' }],
        },
      ],
    },
  ],
};

// A second file renders above main.go, so an ordinary commentable line lands
// under the stationary pointer.
const DIFF_OTHER_FIRST = {
  ...DIFF,
  summary: { fileCount: 2, additions: 2, deletions: 0 },
  files: [
    {
      path: 'util.go',
      status: 'added',
      additions: 1,
      deletions: 0,
      binary: false,
      hunks: [
        {
          header: '@@ -0,0 +1,1 @@',
          lines: [{ kind: 'add', oldLine: null, newLine: 1, content: 'package util' }],
        },
      ],
    },
    ...DIFF.files,
  ],
  tree: [
    { name: 'util.go', path: 'util.go', type: 'file', depth: 0 },
    { name: 'main.go', path: 'main.go', type: 'file', depth: 0 },
  ],
};

const REVIEW = {
  reviewId: 'review-1',
  tenantId: 't1',
  name: 'Add widget',
  targetBranch: 'main',
  sourceBranch: 'feature/widget',
  status: 'READY',
  updatedAt: '2026-01-01T00:00:00Z',
};

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

// No commit to anchor to, so the "commit this change" reason renders.
const DIFF_NO_COMMIT = { ...DIFF, reviewCommits: [] };

function routeLoadTenantDashboard(
  route: Route,
  tenant: string,
  environment: string,
  reviews: (typeof REVIEW)[],
): Promise<void> {
  return fulfillJSON(route, {
    tenant,
    environment,
    apiUrl: 'http://127.0.0.1:1/unreachable',
    user: { tenantId: 't1', userId: 'u1', username: 'operator' },
    reviews,
    panels: [{ tab: 'users' }, { tab: 'reviews' }],
    canCreateReview: false,
    canAdvanceMergeQueue: false,
  });
}

// Opening an env spawns its AI tab, or the occupancy prompt if its lease is
// held; race on whichever appears.
async function dismissAIOccupancyPromptIfShown(
  app: import('../../../pages/index.js').AppShell,
): Promise<void> {
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

// Opening and closing the review detail makes it the active commenting context.
async function openActiveReviewContext(
  app: import('../../../pages/index.js').AppShell,
  tenant: string,
  environment: string,
): Promise<void> {
  await waitForSeededRow(app, tenant, environment);
  await app.sidebar.openTenantDashboard(tenant);
  await app.tenantDashboard.waitForOpen();
  await app.tenantDashboard.selectTab('Reviews');
  await app.tenantDashboard.openReview('Add widget');
  await app.reviewDetailDialog.waitForOpen();
  await app.reviewDetailDialog.locator().getByRole('button', { name: 'Close' }).click();
  await app.reviewDetailDialog.waitForClosed();
  await app.sidebar.openEnvironment(tenant, environment);
  await dismissAIOccupancyPromptIfShown(app);
}

test.describe('diff panel — commenting on a line (#1348, #1388)', () => {
  test('offers to open the Reviews tab when no review is in context', async ({
    app,
    page,
    seededEnv,
  }) => {
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, DIFF);
        return;
      }
      if (body.method === 'LoadTenantDashboard') {
        await routeLoadTenantDashboard(route, seededEnv.tenant, seededEnv.environment, []);
        return;
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await dismissAIOccupancyPromptIfShown(app);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    await app.page.getByText('package main').waitFor({ state: 'visible' });

    // Revealed on hover rather than painted on every row of a dense surface.
    const action = app.page.getByRole('button', { name: 'Comment on line 1 of main.go' });
    await expect(action).toHaveCSS('opacity', '0', withTestBudget());
    await app.page.getByText('package main').hover();
    await expect(action).toHaveCSS('opacity', '1', withTestBudget());

    await action.click();
    await expect(
      app.page.getByText('Open a review from the Reviews tab to comment on this line.'),
    ).toBeVisible(withTestBudget());

    // This reason names a destination on another surface, so it carries an action.
    const openReviewsTab = app.page.getByRole('button', { name: 'Open Reviews tab' });
    await expect(openReviewsTab).toBeVisible(withTestBudget());
    await openReviewsTab.click();

    await app.tenantDashboard.waitForOpen();
    await expect(app.tenantDashboard.tab('Reviews')).toHaveAttribute(
      'aria-selected',
      'true',
      withTestBudget(),
    );
  });

  test('still explains — with no action — that the change needs a commit first', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('diff-comment-no-commit');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadDiff') {
          await fulfillJSON(route, DIFF_NO_COMMIT);
          return;
        }
        if (body.method === 'LoadTenantDashboard') {
          await routeLoadTenantDashboard(route, SEED_TENANT, environment, [REVIEW]);
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, { reviewId: REVIEW.reviewId, review: REVIEW, canComment: true });
          return;
        }
        await route.continue();
      });

      await openActiveReviewContext(app, SEED_TENANT, environment);
      await app.titlebar.toggleReviewPanel();
      await app.reviewPanel.waitForOpen();
      await page.getByText('package main').waitFor({ state: 'visible' });

      await page.getByRole('button', { name: 'Comment on line 1 of main.go' }).click();
      await expect(page.getByText('Commit this change before commenting on it.')).toBeVisible(
        withTestBudget(),
      );
      await expect(page.getByRole('button', { name: 'Open Reviews tab' })).toHaveCount(0);
      await expect(page.getByLabel('New comment')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('still explains — with no action — a lack of comment access', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('diff-comment-no-access');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadDiff') {
          await fulfillJSON(route, DIFF);
          return;
        }
        if (body.method === 'LoadTenantDashboard') {
          await routeLoadTenantDashboard(route, SEED_TENANT, environment, [REVIEW]);
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            reviewId: REVIEW.reviewId,
            review: REVIEW,
            canComment: false,
          });
          return;
        }
        await route.continue();
      });

      await openActiveReviewContext(app, SEED_TENANT, environment);
      await app.titlebar.toggleReviewPanel();
      await app.reviewPanel.waitForOpen();
      await page.getByText('package main').waitFor({ state: 'visible' });

      await page.getByRole('button', { name: 'Comment on line 1 of main.go' }).click();
      await expect(page.getByText('You do not have access to comment on this review.')).toBeVisible(
        withTestBudget(),
      );
      await expect(page.getByRole('button', { name: 'Open Reviews tab' })).toHaveCount(0);
      await expect(page.getByLabel('New comment')).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('starts a new top-level thread anchored to the clicked line', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('diff-comment');
    try {
      let commentInput: Record<string, unknown> | null = null;
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadDiff') {
          await fulfillJSON(route, DIFF);
          return;
        }
        if (body.method === 'LoadTenantDashboard') {
          await routeLoadTenantDashboard(route, SEED_TENANT, environment, [REVIEW]);
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, { reviewId: REVIEW.reviewId, review: REVIEW, canComment: true });
          return;
        }
        if (body.method === 'CreateReviewComment') {
          const parsed = JSON.parse(request.postData() ?? '{}') as {
            args: [Record<string, unknown>];
          };
          commentInput = parsed.args[0];
          await fulfillJSON(route, {
            commentId: 'comment-1',
            creatorUserId: 'operator',
            status: 'OPEN',
            commitId: 'abc123',
            filePath: 'main.go',
            line: 1,
            body: 'what does this do?',
          });
          return;
        }
        await route.continue();
      });

      await openActiveReviewContext(app, SEED_TENANT, environment);
      await app.titlebar.toggleReviewPanel();
      await app.reviewPanel.waitForOpen();
      await page.getByText('package main').waitFor({ state: 'visible' });

      await page.getByRole('button', { name: 'Comment on line 1 of main.go' }).click();
      await page.getByLabel('New comment').fill('what does this do?');
      await page.getByRole('button', { name: 'Comment', exact: true }).click();

      await expect(page.getByLabel('New comment')).toHaveCount(0, withTestBudget());
      expect(commentInput).toMatchObject({
        reviewId: REVIEW.reviewId,
        commitId: 'abc123',
        filePath: 'main.go',
        line: 1,
        body: 'what does this do?',
      });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The reveal belongs to the line, not the pointer position: after a re-read
  // moves the row, no second hover will restore it. The case therefore moves the
  // pointer once and never re-hovers, which would pass for the wrong reason.
  test('the affordance the reader revealed survives a re-read of the diff', async ({
    app,
    page,
    seededEnv,
  }) => {
    let grown = false;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, grown ? DIFF_GROWN : DIFF);
        return;
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await dismissAIOccupancyPromptIfShown(app);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    await page.getByText('package main').waitFor({ state: 'visible' });

    const action = page.getByRole('button', { name: 'Comment on line 1 of main.go' });
    await expect(action).toHaveCSS('opacity', '0', withTestBudget());

    // The only pointer movement in this case.
    await page.getByText('package main').hover();
    await expect(action).toHaveCSS('opacity', '1', withTestBudget());

    grown = true;
    await page.getByText('index 1111111..2222222').waitFor({ state: 'visible' });

    // Converging after the reflow avoids reading a mid-transition frame.
    await expect(action).toHaveCSS('opacity', '1', withTestBudget());
  });

  // The held reveal is re-derived from whatever is under the pointer once it moves.
  test('the affordance is re-observed when the panel re-renders under the pointer', async ({
    app,
    page,
    seededEnv,
  }) => {
    let grown = false;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, grown ? DIFF_GROWN : DIFF);
        return;
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await dismissAIOccupancyPromptIfShown(app);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    await page.getByText('package main').waitFor({ state: 'visible' });

    const action = page.getByRole('button', { name: 'Comment on line 1 of main.go' });
    await page.getByText('package main').hover();
    await expect(action).toHaveCSS('opacity', '1', withTestBudget());

    grown = true;
    await page.getByText('index 1111111..2222222').waitFor({ state: 'visible' });
    await expect(action).toHaveCSS('opacity', '1', withTestBudget());

    // A meta row offers no affordance, so the reveal is released.
    await page.getByText('index 1111111..2222222').hover();
    await expect(action).toHaveCSS('opacity', '0', withTestBudget());

    await page.getByText('package main').hover();
    await expect(action).toHaveCSS('opacity', '1', withTestBudget());
  });

  // Holding the reader's reveal must not suppress the normal hover reveal of the
  // other file's line that the reflow moved under the pointer.
  test('the affordance the reader revealed survives a reflow that brings another file under the pointer', async ({
    app,
    page,
    seededEnv,
  }) => {
    let grown = false;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        await fulfillJSON(route, grown ? DIFF_OTHER_FIRST : DIFF);
        return;
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await dismissAIOccupancyPromptIfShown(app);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    await page.getByText('package main').waitFor({ state: 'visible' });

    const action = page.getByRole('button', { name: 'Comment on line 1 of main.go' });
    await expect(action).toHaveCSS('opacity', '0', withTestBudget());

    // The only pointer movement in this case.
    await page.getByText('package main').hover();
    await expect(action).toHaveCSS('opacity', '1', withTestBudget());

    grown = true;
    await page.getByText('package util').waitFor({ state: 'visible' });

    // The reveal followed main.go's line, not the row now under the pointer.
    await expect(action).toHaveCSS('opacity', '1', withTestBudget());
    await expect(page.getByRole('button', { name: 'Comment on line 1 of util.go' })).toHaveCSS(
      'opacity',
      '1',
      withTestBudget(),
    );
  });

  // LoadDiff is held just past expect's 10s default.
  test('a diff read that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    let held = false;
    await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
      const body = invokeBody(request);
      if (body.method === 'LoadDiff') {
        if (!held) {
          held = true;
          await new Promise((resolve) => setTimeout(resolve, 12_000));
        }
        await fulfillJSON(route, DIFF);
        return;
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await dismissAIOccupancyPromptIfShown(app);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();

    const action = app.page.getByRole('button', { name: 'Comment on line 1 of main.go' });
    await expect(action).toHaveCSS('opacity', '0', withTestBudget());
  });
});
