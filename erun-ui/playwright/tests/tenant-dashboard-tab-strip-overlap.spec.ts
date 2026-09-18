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

// Regression coverage for the tenant dashboard's nine-tab strip overlapping
// itself between ~700px and ~1000px (#2145). The strip is deliberately
// wrapping — the desktop overrides the primitive's single-row layout so a
// narrow <main> gets a second line rather than tabs pushed off-screen — but
// the override never took: the primitive pins the list's height under a
// variant-scoped selector (group-data-[orientation=horizontal]/tabs:h-9),
// which outranks a plain h-auto on specificity, so tailwind-merge kept both
// and the taller of the two lost. The tabs wrapped; the box they wrap inside
// stayed one row tall. The overflowing second row painted over whatever the
// panel rendered beneath it, and at 900px "API log" landed under the
// "+ New review" button — not merely ugly, but unclickable.
//
// What the pre-existing narrow-viewport spec could not see: it asserts each
// tab is fully on screen *horizontally* (narrow-viewport-shell.spec.ts's
// isFullyOnScreen), and a tab on an overflowing second row satisfies that
// perfectly. The failure was vertical the whole time. So this spec asserts
// the strip actually contains the rows it wraps into, that no tab overlaps
// another, that no tab overlaps the panel controls below it, and that every
// tab still wins its own hit test.
//
// Widths cover both boundaries of the reported band (1000 and 700), two
// widths inside it (900 — the reported reproduction — and 820), the sidebar
// collapse breakpoint that sits inside it (758), and one width on each side
// of the band (1200, 640), so a fix that merely moves the broken band rather
// than removing it still fails here. Measured against the pre-fix build,
// every width except 1200 failed — 640 included, so the reported band was
// the vantage the captures were taken from, not the edge of the defect.
//
// Staging mirrors tenant-dashboard-reviews.spec.ts: a throwaway env with an
// apiUrl plus a stubbed LoadTenantDashboard, since the inert harness
// deliberately has no collaboration API to read. All nine panels are
// unrestricted, because the defect is a property of the full-length strip.

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
        // A non-zero pending count widens the Requests tab to "Requests (2)",
        // the label an operator with a real queue actually sees.
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
          { tab: 'gates' },
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

  // Below the collapse breakpoint the sidebar starts folded, so the row has
  // to be reached the way an operator would reach it — reopen, navigate,
  // then let it fold back, so what gets measured is the steady state for
  // this width rather than the momentary reopen.
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

// Boxes overlap when they share real area. 1px of tolerance absorbs sub-pixel
// layout rounding; the defect this guards is tens of pixels of overlap, so
// nothing real hides under that tolerance.
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

// A wrapped row must be part of the strip's own box. A tab hanging below the
// list's bottom edge is the defect itself: the strip kept one row's height
// while its content took two, so the second row paints over the panel
// instead of pushing it down.
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

// layoutViolations is the whole invariant in one sample: every tab inside the
// strip that holds it, no tab over another tab, no tab over the panel's own
// controls, no tab off the side of the window, and every tab still the hit
// target at its own centre. It returns descriptions rather than a boolean so
// a failure names which tab landed where.
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
  // The reported symptom is a tab rendered *underneath* a button, which is
  // the same intersection seen from the other side — only a hit test proves
  // which of the two wins.
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

      // Playwright hit-tests an element before clicking it, so a tab a
      // button has covered fails here rather than silently clicking the
      // button. This is a guard, not the reproduction: against the pre-fix
      // build the 900px overlap was 14px tall and left the tab's own centre
      // clear, so this passed while the geometry above did not. The overlap
      // is what reproduces the report; this keeps the tab clickable as the
      // strip's layout changes again later.
      await app.tenantDashboard.selectTab('API log');
      await expect(app.tenantDashboard.tab('API log')).toHaveAttribute('aria-selected', 'true');
      await expect(app.tenantDashboard.activePanel()).toBeVisible();

      // And back again: the strip must stay usable in both directions, not
      // only for the one tab the report named.
      await app.tenantDashboard.selectTab('Reviews');
      await expect(app.tenantDashboard.newReviewButton()).toBeVisible();
    } finally {
      await page.setViewportSize(DEFAULT_VIEWPORT);
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
