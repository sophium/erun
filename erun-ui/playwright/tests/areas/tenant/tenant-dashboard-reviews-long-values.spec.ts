import type { Request, Route } from '@playwright/test';

import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  removeEnvironment,
  SEED_TENANT,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// Long realistic values must engage the bounding CSS (scrollWidth >
// clientWidth), not merely render.
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

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');

      const row = app.tenantDashboard.reviewsRows().first();
      await expect(row).toBeVisible();
      const rowBox = await boundingBoxOf(row, 'review row');

      const titleElement = row.getByTitle(LONG_TITLE);
      await expect(titleElement).toBeVisible();
      const { clientWidth: titleClientWidth, scrollWidth: titleScrollWidth } =
        await titleElement.evaluate((el) => ({
          clientWidth: el.clientWidth,
          scrollWidth: el.scrollWidth,
        }));
      expect(titleClientWidth).toBeGreaterThan(0);
      expect(titleScrollWidth).toBeGreaterThan(titleClientWidth);

      // Branch names middle-ellipsise rather than wrapping the row tall.
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

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');
      await app.tenantDashboard.openReview(LONG_TITLE);
      await app.reviewDetailDialog.waitForOpen();

      // DialogContent's max-h-[85vh] caps the dialog; the viewport is 1440x1200.
      const dialogBox = await boundingBoxOf(
        app.reviewDetailDialog.locator(),
        'review detail dialog',
      );
      expect(dialogBox.height).toBeLessThanOrEqual(1200 * 0.85 + 2);

      // The comments list must scroll rather than the dialog growing.
      const commentsScroll = app.reviewDetailDialog.locator().locator('.overflow-y-auto').first();
      const { scrollHeight, clientHeight } = await commentsScroll.evaluate((el) => ({
        scrollHeight: el.scrollHeight,
        clientHeight: el.clientHeight,
      }));
      expect(scrollHeight).toBeGreaterThan(clientHeight);

      // A long body shows a preview and "Show more", never a hidden scroll region.
      const dialog = app.reviewDetailDialog.locator();
      await expect(dialog.getByText('End of the long comment body.')).toHaveCount(0);
      const showMore = dialog.getByRole('button', { name: 'Show more' }).first();
      await expect(showMore).toBeVisible();
      await showMore.click();
      await expect(dialog.getByText('End of the long comment body.')).toBeVisible();
      await expect(dialog.getByRole('button', { name: 'Show less' }).first()).toBeVisible();
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

      // The shell has a fixed-width sidebar and no responsive collapse, so widths
      // much below 1000px are unsupported regardless of this tab.
      await page.setViewportSize({ width: 1000, height: 900 });

      await waitForSeededRow(app, SEED_TENANT, environment);
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await app.tenantDashboard.selectTab('Reviews');

      // toBeVisible() passes for off-screen elements, so check viewport geometry.
      const newReview = await boundingBoxOf(app.tenantDashboard.newReviewButton(), 'New review');
      expect(newReview.x + newReview.width).toBeLessThanOrEqual(1000);
      const mine = await boundingBoxOf(app.tenantDashboard.mineFilterButton(), 'Mine filter');
      expect(mine.x + mine.width).toBeLessThanOrEqual(1000);

      const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
      expect(scrollWidth).toBeLessThanOrEqual(1001);

      // The table is deliberately wider than the panel, so the panel must scroll.
      const panelOverflow = await app.tenantDashboard.activePanel().evaluate((el) => ({
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
      }));
      expect(panelOverflow.scrollWidth).toBeGreaterThan(panelOverflow.clientWidth);

      // Restore the default viewport for later specs on the shared backend.
      await page.setViewportSize({ width: 1440, height: 1200 });
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
