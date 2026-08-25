import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import {
  removeEnvironment,
  SEED_TENANT,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// Starting a new top-level review thread from a diff line is what
// ReviewDetailDialog.Comments.tsx used to defer for exactly this reason: only
// the diff panel knows which line was clicked. The Go side
// (CreateReviewComment) is covered by erun-ui/tenant_review_write_test.go;
// these specs cover the desktop's own precondition gating and the happy path.

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

test.describe('diff panel — commenting on a line (#1348)', () => {
  test('explains why commenting is blocked when no review is in context', async ({
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
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await expect(app.page.getByText('package main')).toBeVisible();

    // The affordance is revealed by hovering its own line rather than painted
    // on every row: the diff is the densest reading surface in the app, so a
    // persistent per-line icon would compete with the code it discusses.
    const action = app.page.getByRole('button', { name: 'Comment on line 1 of main.go' });
    await expect(action).toHaveCSS('opacity', '0');
    await app.page.getByText('package main').hover();
    await expect(action).toHaveCSS('opacity', '1');

    await action.click();
    await expect(
      app.page.getByText('Open a review from the Reviews tab to comment on this line.'),
    ).toBeVisible();
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
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [REVIEW],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
            canCreateReview: false,
            canAdvanceMergeQueue: false,
          });
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

      // Open the review to establish it as the active commenting context,
      // then close the (modal) dialog so the diff panel underneath it is
      // reachable — see closeReviewDetail's own comment for why this
      // survives the close.
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');
      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();
      await app.reviewDetailDialog.locator().getByRole('button', { name: 'Close' }).click();
      await app.reviewDetailDialog.waitForClosed();

      await app.sidebar.openEnvironment(SEED_TENANT, environment);
      await app.titlebar.toggleReviewPanel();
      await expect(page.getByText('package main')).toBeVisible();

      await page.getByRole('button', { name: 'Comment on line 1 of main.go' }).click();
      await page.getByLabel('New comment').fill('what does this do?');
      await page.getByRole('button', { name: 'Comment', exact: true }).click();

      await expect(page.getByLabel('New comment')).toHaveCount(0);
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
});
