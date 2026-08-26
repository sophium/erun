import type { Page, Route, Request } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import type { AppShell } from '../pages/index.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// See tenant-dashboard-audit-log.spec.ts for why this stages a throwaway env
// with an apiUrl and stubs the RPCs: the collaboration API these tests stage
// comes from a hosted erun-backend-api the inert harness deliberately has no
// access to, so this exercises the Reviews tab and its detail dialog over the
// stubbed RPC. The Go side is covered by erun-ui/tenant_review_detail_test.go.
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

// openReviewsTabWithOneReview registers exactly one `page.route` matching
// `**/__erun_invoke` for the whole test: Playwright chains overlapping route
// handlers LIFO, and a handler's `route.continue()` sends the request straight
// to the network rather than falling through to an earlier-registered
// handler — so a second `page.route` call for the same pattern would shadow
// this one instead of layering on top of it. replyResult lets a test also
// stub CreateReviewReply without a second registration.
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

  await app.reloadEnvironments();
  await app.sidebar
    .envRowButton(SEED_TENANT, environment)
    .waitFor({ state: 'visible', timeout: 15_000 });
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

      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });
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
      // The composer closes once the reply lands.
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

// Discovery (#1378): an author column and unresolved-thread count readable
// from the row, plus the Mine/Waiting-on-me one-click filters and their own
// distinct "nothing matches this filter" empty state.
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

      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });
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
            // Filtering is proven by the request the desktop makes (the
            // stub cannot re-filter server-side), not by which reviews come
            // back — so it always returns the same review and the assertion
            // is on filterCalls plus the distinct empty state below.
            reviews: mine || waitingOnMe ? [] : [REVIEW],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        await route.continue();
      });

      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });
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
});

