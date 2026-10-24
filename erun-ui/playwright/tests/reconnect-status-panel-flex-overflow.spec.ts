import type { Page } from '@playwright/test';

import { boundingBoxOf } from '../fixtures/boundingBox.js';
import { expect, test } from '../fixtures/erunApp.js';
import { SEED_TENANT, removeEnvironment, seedEnvironment } from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/AppShell.js';

// A `flex-1` item still defaults to `min-width: auto`, so a long target label
// could widen the reconnect panel past the viewport. Proves the label truncates.

const STALE_MESSAGE =
  'ERUN_MCP_UNREACHABLE_STALE: mcp unreachable: the port-forward for pw/env on 127.0.0.1:17999 ' +
  'is not carrying traffic (the local port is held but the edge never answers) — re-establishing it';

// One unbroken run, long enough to widen the header without min-w-0.
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
      // Never fulfilled, holding the panel in its "running" state.
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

// Below 758px the sidebar auto-collapses.
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
        // w-96, clamped by max-w-[calc(100vw-2rem)].
        expect(box.width).toBeLessThanOrEqual(Math.min(384, width - 32) + 1);

        // The label's content overflows and is clipped, not merely short.
        const label = panel.locator('.truncate').first();
        const overflowing = await label.evaluate((el) => el.scrollWidth > el.clientWidth);
        expect(overflowing).toBe(true);
      } finally {
        removeEnvironment(SEED_TENANT, environment);
      }
    });
  });
}
