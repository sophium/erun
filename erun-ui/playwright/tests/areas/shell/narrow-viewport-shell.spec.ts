import type { Locator, Page, Route, Request } from '@playwright/test';

import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// Regression coverage for the shell's fixed-width sidebar starving <main>
// below ~900px: the sidebar never reclamped against the window, so a 640px
// viewport left <main> at a measured 292px, and the Reviews tab's "New
// review" button rendered entirely off-screen with no scrollbar anywhere in
// the ancestor chain to reach it.
//
// See tenant-dashboard-reviews.spec.ts for why this stages a throwaway env
// with an apiUrl and stubs LoadTenantDashboard rather than using a live
// collaboration API the inert harness deliberately has no access to.
//
// A first pass reclamped the sidebar but sized the breakpoint off the
// terminal/diff-review floor (360px), well under the ~495px the dashboard's
// header + tab strip actually need to render without clipping — 640px still
// squeezed <main> to 360px. The intermediate overflow-x-auto wrapper
// (MainPane.tsx) technically made that overflow scrollable, but Chromium
// never painted a discoverable scrollbar for it, so the button was reachable
// only in the DOM, not to a real user. isFullyOnScreen() below asserts the
// element's own bounding box directly rather than accepting a scrollable
// ancestor as a substitute; the actual fix gives the sidebar breakpoint its
// own, wider floor (MIN_DASHBOARD_PANE_WIDTH in app/state.ts) sized off the
// dashboard's measured minimum, so the breakpoint collapses the sidebar
// before <main> is ever squeezed that far.
//
// Known residual gap (not this spec's to fix): TenantDashboardView.tsx's own
// <section> root does not set min-width:0, so the reviews table's explicit
// min-w-[760px] can still make its own overflow-auto panel wider than
// <main> at any width below ~760px, and the same invisible-scrollbar
// symptom applies to it (the Status column can scroll out of reach with no
// visible affordance). That file is owned by a different lane in this
// multi-agent pass; this spec only locks down the header/tab-strip/button
// row, which no longer depends on that table's width.

const DEFAULT_VIEWPORT = { width: 1440, height: 1200 };
// Mirrors app/state.ts's MIN_DASHBOARD_PANE_WIDTH and
// SIDEBAR_COLLAPSE_BREAKPOINT — kept as literals here since this project
// doesn't share a tsconfig path alias with the frontend.
// MIN_DASHBOARD_PANE_WIDTH is the measured, unwrapping minimum width of the
// tenant dashboard's header + tab strip (~495px in Chromium) rounded up with
// a small margin; the breakpoint is MIN_SIDEBAR_WIDTH(248) +
// GRID_DIVIDER_WIDTH(10) + MIN_DASHBOARD_PANE_WIDTH(500).
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
            // Long enough that a starved <main> truncates it mid-word, the
            // symptom quoted in the original report.
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

  // A narrow enough boot needs the sidebar reopened before any sidebar-housed
  // navigation, exactly like an operator would have to reopen it themselves
  // first. Once the dashboard is open the sidebar is no longer needed, so
  // returning it to its automatic collapsed state afterward measures the
  // steady state a user actually lands on for this viewport, not the
  // momentary reopen.
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

// The sidebar can give <main> at most the whole viewport, so once the
// viewport itself is narrower than MIN_DASHBOARD_PANE_WIDTH there is nothing
// left to reclamp even with the sidebar fully collapsed (e.g. 480px) — the
// floor this spec asserts against is capped at the viewport, not the
// constant alone.
function expectedMinMainWidth(viewportWidth: number): number {
  return Math.min(MIN_DASHBOARD_PANE_WIDTH, viewportWidth);
}

// isFullyOnScreen is the "never strand a control" invariant, asserted
// directly against the element's own bounding box rather than inferred from
// document/main scrollWidth. A width-only overflow check let 640px pass
// while the "New review" button rendered sliced by the viewport edge: the
// intermediate overflow-x-auto wrapper (MainPane.tsx) technically made the
// content scrollable, but painted no discoverable scrollbar, so a control
// reachable only by scrolling into invisible space is not reachable at all.
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

        await expect.poll(() => hasHorizontalOverflow(page)).toBe(false);
        await expect
          .poll(() => mainClientWidth(page))
          .toBeGreaterThanOrEqual(expectedMinMainWidth(width));

        const button = app.tenantDashboard.newReviewButton();
        await expect(button).toBeVisible();
        await expect.poll(() => isFullyOnScreen(button, width)).toBe(true);

        // The tab strip's last tab (API log) is the widest-reaching sibling
        // in the row; if it fits, none of the others were clipped either.
        const lastTab = app.tenantDashboard.tab('API log');
        await expect(lastTab).toBeVisible();
        await expect.poll(() => isFullyOnScreen(lastTab, width)).toBe(true);
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

      // This is the original defect verbatim: resizing the OS window, not
      // dragging the splitter, used to leave the sidebar at its stale width
      // and starve <main> to ~292px at 640px. 640px is below the collapse
      // breakpoint, so the sidebar now gives up the column entirely instead
      // of reclamping to a squeeze that still clips the dashboard content.
      await page.setViewportSize({ width: 640, height: 900 });
      await expect.poll(() => sidebarWidthVar(page)).toBe('0px');
      await expect.poll(() => hasHorizontalOverflow(page)).toBe(false);
      await expect
        .poll(() => mainClientWidth(page))
        .toBeGreaterThanOrEqual(expectedMinMainWidth(640));
      const button = app.tenantDashboard.newReviewButton();
      await expect(button).toBeVisible();
      await expect.poll(() => isFullyOnScreen(button, 640)).toBe(true);
      const lastTab = app.tenantDashboard.tab('API log');
      await expect(lastTab).toBeVisible();
      await expect.poll(() => isFullyOnScreen(lastTab, 640)).toBe(true);

      // Below the hard collapse threshold the sidebar must give up the column
      // entirely — there is no room for both it and a usable <main>.
      await page.setViewportSize({ width: 300, height: 900 });
      await expect.poll(() => sidebarWidthVar(page)).toBe('0px');

      // Widening back out restores the sidebar automatically: nothing here
      // was an explicit user override.
      await page.setViewportSize(DEFAULT_VIEWPORT);
      await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');
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
    // Confirm against the rendered <aside>, not only the CSS var, so this
    // also proves the grid column actually reflects the reconciled state.
    await expect(app.sidebar.locator()).not.toHaveCSS('width', '0px');
    await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');

    // Nudge by a pixel repeatedly around the breakpoint; the hysteresis band
    // means the sidebar must hold its current (open) state throughout rather
    // than flicker open/closed on every sub-pixel layout pass.
    for (const width of [
      SIDEBAR_COLLAPSE_BREAKPOINT,
      SIDEBAR_COLLAPSE_BREAKPOINT + 1,
      SIDEBAR_COLLAPSE_BREAKPOINT,
      SIDEBAR_COLLAPSE_BREAKPOINT + 2,
      SIDEBAR_COLLAPSE_BREAKPOINT,
    ]) {
      await page.setViewportSize({ width, height: 900 });
      await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');
    }
    await page.setViewportSize(DEFAULT_VIEWPORT);
  });
});

test.describe('narrow-viewport shell — user intent survives a resize (#1385)', () => {
  test('explicitly opening the sidebar at a narrow width keeps it open across a widen and re-narrow', async ({
    app,
    page,
  }) => {
    // Above the breakpoint the sidebar still shows on its own.
    await page.setViewportSize({ width: 900, height: 900 });
    await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');

    // Auto-collapse it first, then override the automatic decision.
    await page.setViewportSize({ width: 480, height: 900 });
    await expect.poll(() => sidebarWidthVar(page)).toBe('0px');
    await app.titlebar.toggleSidebar();
    await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');

    // The override must survive both directions of a later resize.
    await page.setViewportSize(DEFAULT_VIEWPORT);
    await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');
    await page.setViewportSize({ width: 480, height: 900 });
    await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');

    await page.setViewportSize(DEFAULT_VIEWPORT);
  });

  test('explicitly closing the sidebar on a wide window keeps it closed once narrowed', async ({
    app,
    page,
  }) => {
    await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');
    await app.titlebar.toggleSidebar();
    await expect.poll(() => sidebarWidthVar(page)).toBe('0px');

    // A resize must not reopen a sidebar the operator deliberately closed.
    await page.setViewportSize({ width: 480, height: 900 });
    await expect.poll(() => sidebarWidthVar(page)).toBe('0px');
    await page.setViewportSize(DEFAULT_VIEWPORT);
    await expect.poll(() => sidebarWidthVar(page)).toBe('0px');

    // Restore for later specs in this shared backend.
    await app.titlebar.toggleSidebar();
    await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');
  });
});

test.describe('narrow-viewport shell — collapsed sidebar stays re-openable (#1385)', () => {
  test('the sidebar toggle stays visible and reachable while auto-collapsed', async ({
    app,
    page,
  }) => {
    await page.setViewportSize({ width: 480, height: 900 });
    await expect.poll(() => sidebarWidthVar(page)).toBe('0px');

    const toggle = app.titlebar.toggleButton();
    await expect(toggle).toBeVisible();
    const box = await boundingBoxOf(toggle, 'sidebar toggle button');
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(480);

    await toggle.click();
    await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');

    // Each test gets a fresh browser context, so this override doesn't
    // outlive the test; only the backend-side terminal session's cols do
    // (per terminal-scroll-on-resize.spec.ts), hence restoring the viewport.
    await page.setViewportSize(DEFAULT_VIEWPORT);
    await expect.poll(() => sidebarWidthVar(page)).not.toBe('0px');
  });
});
