import type { Locator, Page, Route, Request } from '@playwright/test';

import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { expect, test, waitForSeededRow, withTestBudget } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// At narrow viewports the sidebar must collapse before <main> is squeezed below the dashboard's
// minimum, keeping controls on screen.

const DEFAULT_VIEWPORT = { width: 1440, height: 1200 };
// Mirrors app/state.ts; the breakpoint is MIN_SIDEBAR_WIDTH(248) + GRID_DIVIDER_WIDTH(10) +
// MIN_DASHBOARD_PANE_WIDTH(500).
const MIN_DASHBOARD_PANE_WIDTH = 500;
const SIDEBAR_COLLAPSE_BREAKPOINT = 758;

function seedDashboardEnvironment(title: string): string {
  const environment = uniqueEnvironmentName(title);
  seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
  return environment;
}

async function fulfillDashboard(route: Route, environment: string): Promise<void> {
  await route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      data: {
        tenant: SEED_TENANT,
        environment,
        apiUrl: 'http://127.0.0.1:1/unreachable',
        user: { tenantId: 't1', userId: 'u1', username: 'operator' },
        canCreateReview: true,
        reviews: [
          {
            reviewId: 'review-1',
            tenantId: 't1',
            name: 'Add the widget rendering pipeline',
            targetBranch: 'main',
            sourceBranch: 'feature/widget-pipeline',
            status: 'READY',
            updatedAt: '2026-01-01T00:00:00Z',
          },
        ],
        panels: [{ tab: 'users' }, { tab: 'reviews' }],
      },
    }),
  });
}

async function sidebarWidthVar(page: Page): Promise<string> {
  return page.evaluate(() =>
    getComputedStyle(document.documentElement).getPropertyValue('--sidebar-width').trim(),
  );
}

async function sidebarCollapsed(app: AppShell): Promise<boolean> {
  return (await app.titlebar.toggleButton().getAttribute('aria-pressed')) === 'false';
}

async function openReviewsTab(app: AppShell, page: Page, environment: string): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadTenantDashboard') {
      await fulfillDashboard(route, environment);
      return;
    }
    await route.continue();
  });

  await waitForSeededRow(app, SEED_TENANT, environment);

  // Reopen the sidebar to navigate, then restore auto-collapse to measure the steady state.
  const wasCollapsed = await sidebarCollapsed(app);
  if (wasCollapsed) {
    await app.titlebar.toggleSidebar();
  }
  await app.sidebar.openTenantDashboard(SEED_TENANT);
  if (wasCollapsed) {
    await app.titlebar.toggleSidebar();
  }
  await app.tenantDashboard.waitForOpen();
  await app.tenantDashboard.selectTab('Reviews');
}

async function hasHorizontalOverflow(page: Page): Promise<boolean> {
  return page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  );
}

async function mainClientWidth(page: Page): Promise<number> {
  return page.evaluate(() => document.querySelector('main')?.clientWidth ?? 0);
}

// Below MIN_DASHBOARD_PANE_WIDTH the floor is capped at the viewport.
function expectedMinMainWidth(viewportWidth: number): number {
  return Math.min(MIN_DASHBOARD_PANE_WIDTH, viewportWidth);
}

// Asserts the element's own box: a scrollable ancestor without a visible scrollbar does not count
// as reachable.
async function isFullyOnScreen(locator: Locator, viewportWidth: number): Promise<boolean> {
  return locator.evaluate((el, vw) => {
    const box = el.getBoundingClientRect();
    return box.x >= 0 && box.x + box.width <= vw && box.width > 0;
  }, viewportWidth);
}

for (const width of [480, 640, 900, 1440]) {
  test.describe(`shell at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test(`<main> is not starved and New review is reachable at ${width}px`, async ({
      app,
      page,
    }) => {
      const environment = seedDashboardEnvironment(`boot-${width}`);
      try {
        await openReviewsTab(app, page, environment);

        await expect.poll(() => hasHorizontalOverflow(page), withTestBudget()).toBe(false);
        await expect
          .poll(() => mainClientWidth(page), withTestBudget())
          .toBeGreaterThanOrEqual(expectedMinMainWidth(width));

        const button = app.tenantDashboard.newReviewButton();
        await expect(button).toBeVisible();
        await expect.poll(() => isFullyOnScreen(button, width), withTestBudget()).toBe(true);

        // The last tab reaches furthest; if it fits, the others do too.
        const lastTab = app.tenantDashboard.tab('API log');
        await expect(lastTab).toBeVisible();
        await expect.poll(() => isFullyOnScreen(lastTab, width), withTestBudget()).toBe(true);
      } finally {
        removeEnvironment(SEED_TENANT, environment);
      }
    });
  });
}

test.describe('narrow-viewport shell — resize behavior (#1385)', () => {
  test('narrowing the window (not the splitter) reclamps the sidebar and keeps <main> reachable', async ({
    app,
    page,
  }) => {
    const environment = seedDashboardEnvironment('resize-narrow');
    try {
      await openReviewsTab(app, page, environment);
      const wideWidth = await sidebarWidthVar(page);
      expect(wideWidth).not.toBe('0px');

      await page.setViewportSize({ width: 640, height: 900 });
      await expect.poll(() => sidebarWidthVar(page), withTestBudget()).toBe('0px');
      await expect.poll(() => hasHorizontalOverflow(page), withTestBudget()).toBe(false);
      await expect
        .poll(() => mainClientWidth(page), withTestBudget())
        .toBeGreaterThanOrEqual(expectedMinMainWidth(640));
      const button = app.tenantDashboard.newReviewButton();
      await expect(button).toBeVisible();
      await expect.poll(() => isFullyOnScreen(button, 640), withTestBudget()).toBe(true);
      const lastTab = app.tenantDashboard.tab('API log');
      await expect(lastTab).toBeVisible();
      await expect.poll(() => isFullyOnScreen(lastTab, 640), withTestBudget()).toBe(true);

      await page.setViewportSize({ width: 300, height: 900 });
      await expect.poll(() => sidebarWidthVar(page), withTestBudget()).toBe('0px');

      // Nothing here was an explicit user override, so the sidebar returns.
      await page.setViewportSize(DEFAULT_VIEWPORT);
      await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');
    } finally {
      await page.setViewportSize(DEFAULT_VIEWPORT);
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('a window parked exactly on the collapse breakpoint does not oscillate', async ({
    app,
    page,
  }) => {
    await page.setViewportSize({ width: SIDEBAR_COLLAPSE_BREAKPOINT, height: 900 });
    await expect(app.sidebar.locator()).not.toHaveCSS('width', '0px');
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');

    // Hysteresis: the sidebar must not flicker around the breakpoint.
    for (const width of [
      SIDEBAR_COLLAPSE_BREAKPOINT,
      SIDEBAR_COLLAPSE_BREAKPOINT + 1,
      SIDEBAR_COLLAPSE_BREAKPOINT,
      SIDEBAR_COLLAPSE_BREAKPOINT + 2,
      SIDEBAR_COLLAPSE_BREAKPOINT,
    ]) {
      await page.setViewportSize({ width, height: 900 });
      await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');
    }
    await page.setViewportSize(DEFAULT_VIEWPORT);
  });
});

test.describe('narrow-viewport shell — user intent survives a resize (#1385)', () => {
  test('explicitly opening the sidebar at a narrow width keeps it open across a widen and re-narrow', async ({
    app,
    page,
  }) => {
    await page.setViewportSize({ width: 900, height: 900 });
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');

    await page.setViewportSize({ width: 480, height: 900 });
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).toBe('0px');
    await app.titlebar.toggleSidebar();
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');

    await page.setViewportSize(DEFAULT_VIEWPORT);
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');
    await page.setViewportSize({ width: 480, height: 900 });
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');

    await page.setViewportSize(DEFAULT_VIEWPORT);
  });

  test('explicitly closing the sidebar on a wide window keeps it closed once narrowed', async ({
    app,
    page,
  }) => {
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');
    await app.titlebar.toggleSidebar();
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).toBe('0px');

    await page.setViewportSize({ width: 480, height: 900 });
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).toBe('0px');
    await page.setViewportSize(DEFAULT_VIEWPORT);
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).toBe('0px');

    // Shared backend: restore for later specs.
    await app.titlebar.toggleSidebar();
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');
  });
});

test.describe('narrow-viewport shell — collapsed sidebar stays re-openable (#1385)', () => {
  test('the sidebar toggle stays visible and reachable while auto-collapsed', async ({
    app,
    page,
  }) => {
    await page.setViewportSize({ width: 480, height: 900 });
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).toBe('0px');

    const toggle = app.titlebar.toggleButton();
    await expect(toggle).toBeVisible();
    const box = await boundingBoxOf(toggle, 'sidebar toggle button');
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(480);

    await toggle.click();
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');

    // The backend terminal session's cols outlive the test, hence restoring the viewport.
    await page.setViewportSize(DEFAULT_VIEWPORT);
    await expect.poll(() => sidebarWidthVar(page), withTestBudget()).not.toBe('0px');
  });
});
