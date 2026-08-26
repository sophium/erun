import type { Request, Route } from '@playwright/test';

import { boundingBoxOf } from '../fixtures/boundingBox.js';
import { expect, test } from '../fixtures/erunApp.js';
import {
  removeEnvironment,
  SEED_TENANT,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// #1378 (#1350 precedent): a fixture that never resembles real data hides
// real overflow bugs — #1350's own fixtures shipped a green gate twice while
// a 5.3KB job command flooded the row. These specs stage genuinely long
// values (long titles, long branch names, many threads, a long comment body)
// and assert the CSS that is supposed to bound them actually engages
// (scrollWidth > clientWidth, not just "the element exists").
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

const LONG_TITLE =
  'Add the widget rendering pipeline for the dashboard export flow so operators can review every generated artifact before it ships to the tenant runtime';
const LONG_BRANCH =
  'feature/1378-desktop-review-loop-usability-and-craft-pass-for-the-tenant-dashboard-reviews-tab';
const LONG_BODY =
  'This is a very long review comment. '.repeat(60) + 'End of the long comment body.';

const LONG_REVIEW = {
  reviewId: 'review-long',
  tenantId: 't1',
  authorUserId: 'u1',
  name: LONG_TITLE,
  targetBranch: `main-${LONG_BRANCH}`,
  sourceBranch: LONG_BRANCH,
  status: 'FAILED',
  unresolvedThreads: 12,
  updatedAt: '2026-01-01T00:00:00Z',
};

function manyThreads(count: number): unknown[] {
  return Array.from({ length: count }, (_, i) => ({
    commentId: `thread-${String(i)}`,
    creatorUserId: `reviewer-${String(i)}`,
    status: 'OPEN',
    commitId: 'abc123',
    filePath: `pkg/file-${String(i)}.go`,
    line: i + 1,
    body: i === 0 ? LONG_BODY : `Thread ${String(i)} comment`,
    createdAt: '2026-01-01T00:00:00Z',
  }));
}

test.describe('tenant dashboard reviews — long values render bounded, not overflowing (#1378)', () => {
  test('a long title and long branch names truncate in the reviews table instead of blowing out the row', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-long-title');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [LONG_REVIEW],
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
      await expect(row).toBeVisible();
      const rowBox = await boundingBoxOf(row, 'review row');

      // table-fixed + truncate: the cell's full text is wider than what is
      // shown, proving the long title/branch clip instead of wrapping the
      // table wide or tall.
      const titleCell = row.locator('td').first();
      const { clientWidth: titleClientWidth, scrollWidth: titleScrollWidth } =
        await titleCell.evaluate((el) => ({
          clientWidth: el.clientWidth,
          scrollWidth: el.scrollWidth,
        }));
      expect(titleClientWidth).toBeGreaterThan(0);
      expect(titleScrollWidth).toBeGreaterThan(titleClientWidth);

      // A single row of a table-fixed table stays roughly one line tall
      // (~40px per DataCell's own padding) even though the underlying values
      // are hundreds of characters long.
      expect(rowBox.height).toBeLessThan(80);

      await expect(row).toContainText('FAILED');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('many threads and a long comment body stay inside the dialog, which scrolls instead of growing without bound', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-long-threads');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [LONG_REVIEW],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
          });
          return;
        }
        if (body.method === 'LoadReviewDetail') {
          await fulfillJSON(route, {
            reviewId: LONG_REVIEW.reviewId,
            review: LONG_REVIEW,
            comments: manyThreads(30),
            unresolvedThreads: 30,
            builds: [],
            canComment: true,
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
      await app.tenantDashboard.openReview(LONG_TITLE);
      await app.reviewDetailDialog.waitForOpen();

      // The dialog's own frame (DialogContent's max-h-[85vh]) caps the whole
      // surface regardless of how many threads it holds. The suite's config
      // viewport is 1440x1200 and this test never changes it.
      const dialogBox = await boundingBoxOf(
        app.reviewDetailDialog.locator(),
        'review detail dialog',
      );
      expect(dialogBox.height).toBeLessThanOrEqual(1200 * 0.85 + 2);

      // 30 threads is far more than fits in that frame, so the comments list
      // itself must be the thing scrolling, not the dialog growing past its cap.
      const commentsScroll = app.reviewDetailDialog.locator().locator('.overflow-y-auto').first();
      const { scrollHeight, clientHeight } = await commentsScroll.evaluate((el) => ({
        scrollHeight: el.scrollHeight,
        clientHeight: el.clientHeight,
      }));
      expect(scrollHeight).toBeGreaterThan(clientHeight);

      // The long comment body on the first thread is bounded by its own
      // max-h-48 overflow-y-auto rather than pushing every later thread off
      // the fold.
      const longBody = app.reviewDetailDialog.locator().getByText('End of the long comment body.');
      await expect(longBody).toBeVisible();
      const bodyBox = await longBody.evaluate((el) => {
        const scrollBox = el.closest('p');
        if (!scrollBox) {
          throw new Error('comment body paragraph not found');
        }
        return { scrollHeight: scrollBox.scrollHeight, clientHeight: scrollBox.clientHeight };
      });
      expect(bodyBox.scrollHeight).toBeGreaterThan(bodyBox.clientHeight);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a narrow viewport keeps the reviews tab usable: no horizontal overflow, actions still reachable', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('reviews-narrow-viewport');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = invokeBody(request);
        if (body.method === 'LoadTenantDashboard') {
          await fulfillJSON(route, {
            tenant: SEED_TENANT,
            environment,
            apiUrl: 'http://127.0.0.1:1/unreachable',
            user: { tenantId: 't1', userId: 'u1', username: 'operator' },
            reviews: [LONG_REVIEW],
            panels: [{ tab: 'users' }, { tab: 'reviews' }],
            canCreateReview: true,
          });
          return;
        }
        await route.continue();
      });

      await page.setViewportSize({ width: 640, height: 900 });

      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');

      await expect(app.tenantDashboard.newReviewButton()).toBeVisible();
      await expect(app.tenantDashboard.mineFilterButton()).toBeVisible();

      const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
      expect(scrollWidth).toBeLessThanOrEqual(641);

      // Restore the config default viewport for later specs in the singleton backend.
      await page.setViewportSize({ width: 1440, height: 1200 });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
