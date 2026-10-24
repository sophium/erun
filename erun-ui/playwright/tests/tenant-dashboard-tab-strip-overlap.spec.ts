import type { Page, Route, Request } from '@playwright/test';

import { boundingBoxOf, type ElementBox } from '../fixtures/boundingBox.js';
import { expect, test, waitForSeededRow } from '../fixtures/erunApp.js';
import type { AppShell, TabBox } from '../pages/index.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// The wrapping tab strip must grow to hold its rows between ~700px and ~1000px:
// the primitive's variant-scoped h-9 outranks h-auto, so a second row would
// paint over the panel. Widths cover both band edges, the inside, the 758px
// sidebar breakpoint, and one width either side. The tab count is pinned.

const DEFAULT_VIEWPORT = { width: 1440, height: 1200 };

const REPORTED_TAB_COUNT = 9;

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
        // Widens the tab to "Requests (2)".
        pendingInviteRequestCount: 2,
        mineReviewCount: 1,
        waitingOnMeReviewCount: 1,
        reviews: [
          {
            reviewId: 'review-1',
            tenantId: 't1',
            name: 'Add the widget rendering pipeline',
            targetBranch: 'main',
            sourceBranch: 'feature/widget-pipeline',
            status: 'READY',
            unresolvedThreads: 1,
            updatedAt: '2026-01-01T00:00:00Z',
          },
        ],
        panels: [
          { tab: 'users' },
          { tab: 'reviews' },
          { tab: 'queue' },
          { tab: 'builds' },
          { tab: 'audit' },
          { tab: 'registration' },
          { tab: 'requests' },
          { tab: 'api-log' },
        ],
      },
    }),
  });
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

  // Reopen a collapsed sidebar to navigate, then fold it back to measure the
  // steady state.
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
  await expect(app.tenantDashboard.newReviewButton()).toBeVisible();
}

// 1px absorbs sub-pixel rounding; the defect is tens of pixels.
function overlaps(a: ElementBox, b: ElementBox): boolean {
  const horizontal = Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x);
  const vertical = Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y);
  return horizontal > 1 && vertical > 1;
}

function describeBox(label: string, box: ElementBox): string {
  const right = box.x + box.width;
  const bottom = box.y + box.height;
  return `${label} [x ${box.x.toFixed(0)}-${right.toFixed(0)}, y ${box.y.toFixed(0)}-${bottom.toFixed(0)}]`;
}

// A tab below the list's bottom edge means the strip kept one row's height.
function escapesStrip(list: ElementBox, tab: TabBox): boolean {
  return tab.y < list.y - 1 || tab.y + tab.height > list.y + list.height + 1;
}

function offScreen(tab: TabBox, viewportWidth: number): boolean {
  return tab.x < -1 || tab.x + tab.width > viewportWidth + 1 || tab.width <= 0;
}

function tabPairViolations(tabs: TabBox[]): string[] {
  const found: string[] = [];
  for (let i = 0; i < tabs.length; i += 1) {
    for (let j = i + 1; j < tabs.length; j += 1) {
      const a = tabs[i];
      const b = tabs[j];
      if (a && b && overlaps(a, b)) {
        found.push(`${describeBox(a.label, a)} overlaps ${describeBox(b.label, b)}`);
      }
    }
  }
  return found;
}

async function panelControls(app: AppShell): Promise<{ label: string; box: ElementBox }[]> {
  const dashboard = app.tenantDashboard;
  return [
    {
      label: '"New review" button',
      box: await boundingBoxOf(dashboard.newReviewButton(), 'New review button'),
    },
    {
      label: '"Mine" filter',
      box: await boundingBoxOf(dashboard.mineFilterButton(), 'Mine filter'),
    },
    {
      label: '"Waiting on me" filter',
      box: await boundingBoxOf(dashboard.waitingOnMeFilterButton(), 'Waiting on me filter'),
    },
  ];
}

// layoutViolations describes every broken invariant so a failure names the tab.
async function layoutViolations(app: AppShell, viewportWidth: number): Promise<string[]> {
  const { list, tabs } = await app.tenantDashboard.tabStripGeometry();
  const controls = await panelControls(app);
  const found: string[] = [];

  if (tabs.length !== REPORTED_TAB_COUNT) {
    found.push(
      `the strip renders ${String(tabs.length)} tabs, expected ${String(REPORTED_TAB_COUNT)}`,
    );
  }
  for (const tab of tabs) {
    if (escapesStrip(list, tab)) {
      found.push(`${describeBox(tab.label, tab)} escapes the ${describeBox('tab strip', list)}`);
    }
    if (offScreen(tab, viewportWidth)) {
      found.push(
        `${describeBox(tab.label, tab)} is not fully on screen (viewport ${String(viewportWidth)}px)`,
      );
    }
    found.push(
      ...controls
        .filter((control) => overlaps(tab, control.box))
        .map(
          (control) =>
            `${describeBox(tab.label, tab)} overlaps ${describeBox(control.label, control.box)}`,
        ),
    );
  }
  // Only a hit test proves which of two intersecting elements wins.
  return [...found, ...tabPairViolations(tabs), ...(await app.tenantDashboard.coveredTabs())];
}

for (const { width, note } of [
  { width: 1200, note: 'above the reported band' },
  { width: 1000, note: 'the upper boundary of the reported band' },
  { width: 900, note: 'the reported reproduction' },
  { width: 820, note: 'inside the reported band' },
  { width: 758, note: 'the sidebar collapse breakpoint, inside the band' },
  { width: 700, note: 'the lower boundary of the reported band' },
  { width: 640, note: 'below the reported band' },
]) {
  test.describe(`tenant dashboard tab strip at ${String(width)}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test(`the nine-tab strip does not overlap itself or the panel at ${String(width)}px (${note})`, async ({
      app,
      page,
    }) => {
      const environment = seedDashboardEnvironment(`tabstrip-${String(width)}`);
      try {
        await openReviewsTab(app, page, environment);
        await expect.poll(() => layoutViolations(app, width)).toEqual([]);
      } finally {
        removeEnvironment(SEED_TENANT, environment);
      }
    });
  });
}

test.describe('tenant dashboard tab strip — the reported tab stays clickable (#2145)', () => {
  test.use({ viewport: { width: 900, height: 900 } });

  test('API log can still be opened at 900px', async ({ app, page }) => {
    const environment = seedDashboardEnvironment('tabstrip-reachable');
    try {
      await openReviewsTab(app, page, environment);

      // A guard, not the reproduction: a small overlap can leave the centre clear.
      await app.tenantDashboard.selectTab('API log');
      await expect(app.tenantDashboard.tab('API log')).toHaveAttribute('aria-selected', 'true');
      await expect(app.tenantDashboard.activePanel()).toBeVisible();

      await app.tenantDashboard.selectTab('Reviews');
      await expect(app.tenantDashboard.newReviewButton()).toBeVisible();
    } finally {
      await page.setViewportSize(DEFAULT_VIEWPORT);
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
