import type { Page } from '@playwright/test';

import { boundingBoxOf } from '../fixtures/boundingBox.js';
import { expect, test } from '../fixtures/erunApp.js';
import { SEED_TENANT, removeEnvironment, seedEnvironment } from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/AppShell.js';

// Regression coverage for #431: the reconnect status panel's header
// (ReconnectStatusPanel.tsx's ReconnectStatusHeader) is a flex row whose
// text column carries `flex-1`. A `flex-1` item still defaults to
// `min-width: auto`, so a long, unbroken target label refused to shrink and
// could force the panel (`fixed bottom-4 right-4 w-96
// max-w-[calc(100vw-2rem)]`) wider than the viewport -- the original
// report's horizontal-overflow symptom, before the panel was redesigned into
// this non-blocking, per-env surface. The header text column already
// carries `min-w-0 flex-1` and the target label itself carries `truncate`;
// this proves the chain actually engages (the label's content overflows its
// own box and is clipped) rather than the panel simply growing to fit it.

const STALE_MESSAGE =
  'ERUN_MCP_UNREACHABLE_STALE: mcp unreachable: the port-forward for pw/env on 127.0.0.1:17999 ' +
  'is not carrying traffic (the local port is held but the edge never answers) — re-establishing it';

// One unbroken alphanumeric run, long enough that -- without the min-w-0
// chain -- it would drag the panel's fixed-width header wider than its own
// box, exactly the shape of a long environment/orchestrator name.
function longEnvironmentName(): string {
  return 'reconnectoverflow' + Math.random().toString(36).slice(2, 8) + 'p'.repeat(70);
}

async function stubReconnectFlow(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'LoadDiff') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ error: STALE_MESSAGE }),
      });
      return;
    }
    if (body.method === 'ReconnectMCP') {
      // Deliberately never fulfilled: the panel's "running" state is what
      // this spec needs held open long enough to inspect its layout.
      return;
    }
    await route.continue();
  });
}

async function hasHorizontalOverflow(page: Page): Promise<boolean> {
  return page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  );
}

// 480px sits below the shell's own sidebar auto-collapse breakpoint (758px,
// `narrow-viewport-shell.spec.ts`), so the sidebar starts collapsed there and
// its environment row is not reachable until `Titlebar.toggleSidebar()` opens
// it back up first.
const SIDEBAR_COLLAPSE_BREAKPOINT = 758;

async function openEnvironmentAtWidth(
  app: AppShell,
  environment: string,
  width: number,
): Promise<void> {
  if (width < SIDEBAR_COLLAPSE_BREAKPOINT) {
    await app.titlebar.toggleSidebar();
  }
  await app.sidebar.openEnvironment(SEED_TENANT, environment);
}

for (const width of [480, 900]) {
  test.describe(`reconnect status panel at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test(`a long environment name does not widen the panel past ${width}px, and truncates instead`, async ({
      app,
      page,
    }) => {
      const environment = longEnvironmentName();
      seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
      try {
        await stubReconnectFlow(page);
        await openEnvironmentAtWidth(app, environment, width);
        await app.titlebar.toggleReviewPanel();

        const alert = app.reviewPanel.errorAlerts();
        await expect(alert).toHaveCount(1);
        await alert.getByRole('button', { name: 'Reconnect…' }).click();

        const confirmDialog = page.getByRole('dialog', { name: 'Reconnect to environment?' });
        await expect(confirmDialog).toBeVisible();
        await confirmDialog.getByRole('button', { name: 'Reconnect', exact: true }).click();

        const panel = page.locator('[data-reconnect-status="running"]');
        await expect(panel).toBeVisible();

        await expect.poll(() => hasHorizontalOverflow(page)).toBe(false);
        const box = await boundingBoxOf(panel, `reconnect status panel at ${width}px`);
        expect(box.x).toBeGreaterThanOrEqual(0);
        expect(box.x + box.width).toBeLessThanOrEqual(width);
        // The panel never grows past its own intended cap (w-96 = 384px,
        // clamped further by max-w-[calc(100vw-2rem)] on narrow viewports) to
        // accommodate the long name -- proving the header didn't drag it wider.
        expect(box.width).toBeLessThanOrEqual(Math.min(384, width - 32) + 1);

        // And the label itself is genuinely truncated (its content wants more
        // room than it was given), not merely short enough to fit -- this is
        // what min-w-0 on the flex-1 ancestor makes possible; without it, the
        // ancestor would have grown to fit instead of clipping this element.
        const label = panel.locator('.truncate').first();
        const overflowing = await label.evaluate((el) => el.scrollWidth > el.clientWidth);
        expect(overflowing).toBe(true);
      } finally {
        removeEnvironment(SEED_TENANT, environment);
      }
    });
  });
}
