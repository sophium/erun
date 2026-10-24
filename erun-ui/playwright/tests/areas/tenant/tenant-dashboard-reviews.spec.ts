import type { Page, Route, Request } from '@playwright/test';

import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The collaboration API is hosted and unreachable from the harness, so the RPCs
// are stubbed.
function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

function invokeBody(request: Request): { method: string } {
  return JSON.parse(request.postData() ?? '{}') as { method: string };
}

async function fulfillJSON(route: Route, data: unknown): Promise<void> {
  await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) });
}

const REVIEW = {
  reviewId: 'review-1',
  tenantId: 't1',
  name: 'Add widget',
  targetBranch: 'main',
  sourceBranch: 'feature/widget',
  status: 'READY',
  updatedAt: '2026-01-01T00:00:00Z',
};

const ROOT_COMMENT = {
  commentId: 'comment-1',
  creatorUserId: 'reviewer-1',
  status: 'OPEN',
  commitId: 'abc123',
  filePath: 'main.go',
  line: 42,
  body: 'nit: rename this',
  createdAt: '2026-01-01T00:00:00Z',
};

const NEW_REPLY = {
  commentId: 'comment-2',
  creatorUserId: 'operator',
  status: 'OPEN',
  parentCommentId: 'comment-1',
  commitId: 'abc123',
  filePath: 'main.go',
  line: 42,
  body: 'fixed, thanks',
  createdAt: '2026-01-01T00:05:00Z',
};

function reviewDetail(comments: unknown[]): Record<string, unknown> {
  return {
    reviewId: REVIEW.reviewId,
    review: REVIEW,
    comments,
    builds: [
      {
        buildId: 'build-1',
        reviewId: REVIEW.reviewId,
        successful: true,
        commitId: 'abc123',
        version: '1.2.3',
      },
    ],
    queuePosition: 1,
    canComment: true,
  };
}

// Registers a single `page.route` for the test: a second handler for the same
// pattern would shadow this one, so replyResult covers CreateReviewReply too.
async function openReviewsTabWithOneReview(
  app: AppShell,
  page: Page,
  environment: string,
  comments: unknown[],
  replyResult?: { data?: unknown; status?: number },
): Promise<void> {
  let detailCalls = 0;
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = invokeBody(request);
    if (body.method === 'LoadTenantDashboard') {
      await fulfillJSON(route, {
        tenant: SEED_TENANT,
        environment,
        apiUrl: 'http://127.0.0.1:1/unreachable',
        user: { tenantId: 't1', userId: 'u1', username: 'operator' },
        reviews: [REVIEW],
        panels: [
          { tab: 'users' },
          { tab: 'reviews' },
          { tab: 'queue' },
          { tab: 'builds' },
          { tab: 'audit' },
        ],
      });
      return;
    }
    if (body.method === 'LoadReviewDetail') {
      detailCalls += 1;
      await fulfillJSON(
        route,
        reviewDetail(detailCalls > 1 ? [ROOT_COMMENT, NEW_REPLY] : comments),
      );
      return;
    }
    if (body.method === 'CreateReviewReply' && replyResult) {
      if (replyResult.status && replyResult.status >= 400) {
        await route.fulfill({
          status: replyResult.status,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'platform api: conflict' }),
        });
      } else {
        await fulfillJSON(route, replyResult.data);
      }
      return;
    }
    await route.continue();
  });

  await waitForSeededRow(app, SEED_TENANT, environment);
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  await app.tenantDashboard.waitForOpen();
  await app.tenantDashboard.selectTab('Reviews');
}

test.describe('tenant dashboard — reviews tab and detail dialog (#1199)', () => {
  test('lists a review with a status badge and opens its detail, builds, and comment thread', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-populated');
    try {
      await openReviewsTabWithOneReview(app, page, environment, [ROOT_COMMENT]);

      await expect(app.tenantDashboard.reviewsRows()).toHaveCount(1);
      await expect(app.tenantDashboard.reviewsTable()).toContainText('READY');

      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();
      await expect(app.reviewDetailDialog.locator()).toContainText('feature/widget');
      await expect(app.reviewDetailDialog.locator()).toContainText('main');
      await expect(app.reviewDetailDialog.locator()).toContainText('queue position 1');
      await expect(app.reviewDetailDialog.locator()).toContainText('abc123');
      await expect(app.reviewDetailDialog.locator()).toContainText('nit: rename this');
      await expect(app.reviewDetailDialog.locator()).toContainText('reviewer-1');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('shows a purpose-built empty state, not an input-styled box, when there are no reviews', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-empty');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');

      await expect(app.tenantDashboard.reviewsEmptyState()).toBeVisible();
      await expect(app.tenantDashboard.reviewsTable()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('replying to a thread appends it, attributed to the signed-in user', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-reply');
    try {
      await openReviewsTabWithOneReview(app, page, environment, [ROOT_COMMENT], {
        data: NEW_REPLY,
      });

      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();

      await app.reviewDetailDialog.reply(0);
      await app.reviewDetailDialog.fillReply('fixed, thanks');
      await app.reviewDetailDialog.sendReply();

      await expect(app.reviewDetailDialog.locator()).toContainText('fixed, thanks');
      await expect(app.reviewDetailDialog.locator()).toContainText('operator');
      await expect(app.reviewDetailDialog.replyInput()).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a failed reply submit keeps the draft text (Nielsen #3, user control)', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-reply-failure');
    try {
      await openReviewsTabWithOneReview(app, page, environment, [ROOT_COMMENT], {
        status: 500,
      });

      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();

      await app.reviewDetailDialog.reply(0);
      await app.reviewDetailDialog.fillReply('fixed, thanks');
      await app.reviewDetailDialog.sendReply();

      await expect(app.reviewDetailDialog.locator()).toContainText('platform api: conflict');
      await expect(app.reviewDetailDialog.replyInput()).toHaveValue('fixed, thanks');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

test.describe('tenant dashboard — reviews discovery (#1378)', () => {
  test('the row shows the signed-in author as "You" and the unresolved-thread count without opening the review', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-author-threads');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [{ ...REVIEW, authorUserId: 'u1', unresolvedThreads: 2 }],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');

      const row = app.tenantDashboard.reviewsRows().first();
      await expect(row).toContainText('You');
      await expect(row).toContainText('2 unresolved');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('Mine and Waiting on me apply as one-click filters and reload the dashboard with them', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-filter');
    try {
      const filterCalls: { authorUserId?: string; reviewerUserId?: string }[] = [];
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = JSON.parse(request.postData() ?? '{}') as {
          method: string;
          args?: [{ reviewFilterMine?: boolean; reviewFilterWaitingOnMe?: boolean }];
        };
        if (body.method === 'LoadTenantDashboard') {
          const input = body.args?.[0];
          const mine = Boolean(input?.reviewFilterMine);
          const waitingOnMe = Boolean(input?.reviewFilterWaitingOnMe);
          filterCalls.push({
            authorUserId: mine ? 'u1' : undefined,
            reviewerUserId: waitingOnMe ? 'u1' : undefined,
          });
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            // The stub cannot filter, so filtering is proven by the request made.
            reviews: mine || waitingOnMe ? [] : [REVIEW],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');
      await expect(app.tenantDashboard.reviewsRows()).toHaveCount(1);

      await app.tenantDashboard.mineFilterButton().click();
      await expect(app.tenantDashboard.reviewsFilteredEmptyState()).toBeVisible();
      await expect(app.tenantDashboard.reviewsEmptyState()).toHaveCount(0);
      await expect(app.tenantDashboard.mineFilterButton()).toHaveAttribute('aria-pressed', 'true');

      await app.tenantDashboard.clearReviewFilterButton().click();
      await expect(app.tenantDashboard.reviewsRows()).toHaveCount(1);
      await expect(app.tenantDashboard.mineFilterButton()).toHaveAttribute('aria-pressed', 'false');

      await app.tenantDashboard.waitingOnMeFilterButton().click();
      await expect(app.tenantDashboard.reviewsFilteredEmptyState()).toBeVisible();

      expect(filterCalls).toEqual(
        expect.arrayContaining([
          { authorUserId: 'u1', reviewerUserId: undefined },
          { authorUserId: undefined, reviewerUserId: undefined },
          { authorUserId: undefined, reviewerUserId: 'u1' },
        ]),
      );
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // Finished reviews accumulate forever, so the list opens on OPEN+MERGE.
  test('the status chips open on OPEN+MERGE and narrow the list to them', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('reviews-status-filter');
    try {
      const openReview = { ...REVIEW, reviewId: 'review-open', name: 'Add widget', status: 'OPEN' };
      const mergedReview = {
        ...REVIEW,
        reviewId: 'review-merged',
        name: 'Ship widget',
        status: 'MERGED',
      };
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = JSON.parse(request.postData() ?? '{}') as { method: string };
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [openReview, mergedReview],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');

      await expect(app.tenantDashboard.reviewStatusFilterGroup()).toBeVisible();
      await expect(app.tenantDashboard.reviewStatusFilterButton('OPEN')).toHaveAttribute(
        'aria-pressed',
        'true',
      );
      await expect(app.tenantDashboard.reviewStatusFilterButton('MERGE')).toHaveAttribute(
        'aria-pressed',
        'true',
      );
      await expect(app.tenantDashboard.reviewStatusFilterButton('MERGED')).toHaveAttribute(
        'aria-pressed',
        'false',
      );
      await expect(app.tenantDashboard.reviewsRows()).toHaveCount(1);
      // Both numbers, so a filtered count is not misread as the whole history.
      await expect(app.tenantDashboard.reviewCountText()).toHaveText('1 of 2 reviews');

      await app.tenantDashboard.reviewStatusFilterButton('MERGED').click();
      await expect(app.tenantDashboard.reviewsRows()).toHaveCount(2);
      await expect(app.tenantDashboard.reviewCountText()).toHaveText('2 reviews');

      // The chips narrow independently, not as mutually exclusive tabs.
      await app.tenantDashboard.reviewStatusFilterButton('OPEN').click();
      await expect(app.tenantDashboard.reviewsRows()).toHaveCount(1);
      await expect(app.tenantDashboard.reviewCountText()).toHaveText('1 of 2 reviews');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

// Row and dialog come from different reads; a row without an enriched count
// must not show a dash while the dialog reports one.
test.describe('tenant dashboard — the reviews row and the detail dialog agree on thread count', () => {
  test('a row with no computed count reports the dialog’s count once the review has been opened', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-count-agreement');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            // No unresolvedThreads: the best-effort enrichment yielded no count.
            reviews: [{ ...REVIEW }],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            ...reviewDetail([{ ...ROOT_COMMENT, status: 'OPEN' }]),
            unresolvedThreads: 1,
            canResolveComments: true,
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');

      const row = app.tenantDashboard.reviewsRows().first();
      await expect(row).toContainText('Unknown');
      await expect(row).not.toContainText('1 unresolved');

      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();
      await expect(app.reviewDetailDialog.locator()).toContainText('1 unresolved');

      // The row is inert behind the modal, so read it after closing.
      await page.keyboard.press('Escape');
      await app.reviewDetailDialog.waitForClosed();
      await expect(row).toContainText('1 unresolved');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

test.describe('tenant dashboard — resolving a comment thread (#1378)', () => {
  test('resolving a thread flips its status, the action to Reopen, and the header count', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-resolve');
    try {
      let resolved = false;
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [REVIEW],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            ...reviewDetail([{ ...ROOT_COMMENT, status: resolved ? 'CLOSED' : 'OPEN' }]),
            unresolvedThreads: resolved ? 0 : 1,
            canResolveComments: true,
          });
          return;
        }
        if (body.method === 'ResolveReviewComment') {
          resolved = true;
          await fulfillJSON(route, { ...ROOT_COMMENT, status: 'CLOSED' });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');
      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();

      await expect(app.reviewDetailDialog.locator()).toContainText('1 unresolved');
      await expect(app.reviewDetailDialog.locator()).toContainText('Unresolved');

      await app.reviewDetailDialog.resolveButton(0).click();

      await expect(app.reviewDetailDialog.locator()).toContainText('All resolved');
      await expect(app.reviewDetailDialog.locator()).toContainText('Resolved');
      await expect(app.reviewDetailDialog.reopenButton(0)).toBeVisible();
      await expect(app.reviewDetailDialog.resolveButton(0)).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('the row shows an avatar and resolved author name, the filter buttons show counts, and the dialog gives the branch line and Resolve their own treatment', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-craft-pass');
    const longBranch =
      'feature/1378-desktop-review-loop-usability-and-craft-pass-for-the-tenant-dashboard-reviews-tab';
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [
              {
                ...REVIEW,
                authorUserId: 'u2',
                authorUsername: 'Pat Reviewer',
                unresolvedThreads: 1,
              },
            ],
            mineReviewCount: 3,
            waitingOnMeReviewCount: 5,
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            reviewId: REVIEW.reviewId,
            review: { ...REVIEW, sourceBranch: longBranch, targetBranch: `main-${longBranch}` },
            comments: [ROOT_COMMENT],
            unresolvedThreads: 1,
            builds: [],
            canComment: true,
            canResolveComments: true,
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');

      const row = app.tenantDashboard.reviewsRows().first();
      await expect(row).toContainText('Pat Reviewer');
      await expect(row).not.toContainText('u2');
      // The avatar is aria-hidden, so it has no accessible name to query.
      await expect(row.locator('span[aria-hidden="true"]').filter({ hasText: 'PR' })).toBeVisible();

      await expect(app.tenantDashboard.mineFilterButton()).toContainText('3');
      await expect(app.tenantDashboard.waitingOnMeFilterButton()).toContainText('5');

      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();
      const dialog = app.reviewDetailDialog.locator();

      const dialogBox = await boundingBoxOf(dialog, 'review detail dialog');
      expect(dialogBox.width).toBeGreaterThan(700);

      // The branch middle-ellipsises, with the full value in its title.
      await expect(dialog.getByText(longBranch, { exact: true })).toHaveCount(0);
      await expect(dialog.locator(`[title="${longBranch}"]`)).toHaveCount(1);

      await expect(dialog.getByRole('button', { name: 'Resolve' }).first()).toHaveAttribute(
        'data-variant',
        'default',
      );
      await expect(dialog.getByRole('button', { name: 'Reply' }).first()).toHaveAttribute(
        'data-variant',
        'outline',
      );
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('hides the resolve action and names the missing access when the caller cannot resolve threads', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-resolve-restricted');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [REVIEW],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            ...reviewDetail([ROOT_COMMENT]),
            unresolvedThreads: 1,
            canResolveComments: false,
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');
      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();

      await expect(app.reviewDetailDialog.locator()).toContainText('Unresolved');
      await expect(app.reviewDetailDialog.resolveButton(0)).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

// Only checks that BuildProfileDialog nests in ReviewDetailDialog and closing
// it leaves the outer dialog open.
test.describe('tenant dashboard — review build profile (#2274)', () => {
  test('opens over the review detail dialog and closes back to it', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('review-build-profile');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [REVIEW],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            ...reviewDetail([ROOT_COMMENT]),
            builds: [
              {
                buildId: 'build-gate-1',
                reviewId: REVIEW.reviewId,
                successful: true,
                commitId: 'abc123',
                version: '',
                profile: {
                  durationSeconds: 12,
                  totalStepCount: 1,
                  topSteps: [{ name: 'erun-devops', durationSeconds: 11 }],
                },
              },
            ],
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');
      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();

      await app.reviewDetailDialog.buildProfileButtonFor('build-gate-1').click();
      await app.buildProfileDialog.waitForOpen();
      await expect(app.buildProfileDialog.locator()).toContainText('erun-devops');
      // No cgroup reading, so cells read "Not available", never zero.
      await expect(app.buildProfileDialog.notAvailableNotice()).toBeVisible();

      await page.keyboard.press('Escape');
      await app.buildProfileDialog.waitForClosed();
      await expect(app.reviewDetailDialog.locator()).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The denial must carry the grant with the caller's own user id.
  test('a review the caller may not open hands over the grant that lifts it', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-restricted-remedy');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [REVIEW],
            panels: [
              { tab: 'users' },
              { tab: 'reviews' },
              { tab: 'queue' },
              { tab: 'builds' },
              { tab: 'audit' },
            ],
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            reviewId: REVIEW.reviewId,
            restricted: 'GET /v1/reviews/{review_id}',
            accessRemedies: {
              'GET /v1/reviews/{review_id}': {
                command: 'erun platform user grant-role --user-id user-1 --role-id role-reviewer',
                roleName: 'Reviewer',
              },
            },
          });
          return;
        }
        await route.continue();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');
      await app.tenantDashboard.openReview('Add widget');
      await app.reviewDetailDialog.waitForOpen();

      await expect(app.reviewDetailDialog.locator()).toContainText(
        'You do not have access to this review',
      );
      await expect(app.reviewDetailDialog.locator()).toContainText(
        'erun platform user grant-role --user-id user-1 --role-id role-reviewer',
      );
      await expect(app.reviewDetailDialog.locator()).toContainText('Reviewer');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
