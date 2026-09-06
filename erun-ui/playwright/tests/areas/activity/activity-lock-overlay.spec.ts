import { test, expect } from '../../../fixtures/erunApp.js';
import type { Page, Request } from '@playwright/test';
import type { AppShell } from '../../../pages/index.js';

// The top-right-anchored ActivityLockOverlay used to be dragged off-screen and
// clipped when the terminal pane was starved of width (wide sidebar and/or
// narrow window). This spec reproduces that starvation and asserts the overlay
// stays fully inside the viewport.
test.describe('deploy-progress overlay stays on-screen (#713)', () => {
  test('the activity-lock overlay is not clipped when the terminal pane is starved', async ({
    app,
    page,
    seededEnv,
  }) => {
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    // The overlay renders over whichever terminal session is active, so pin the
    // ERun tab (the env default) active first.
    const erunTab = page.getByRole('tab', { name: 'ERun', exact: true });
    await erunTab.waitFor({ state: 'visible', timeout: 15_000 });
    await erunTab.click();

    const sessionId = await discoverSelectedSessionId(app, page);
    expect(sessionId).toBeGreaterThan(0);

    // Starve the terminal pane: the sidebar (~348px incl. handle) plus the old
    // 360px column minimum needed ~708px of width, so 640px forced the overflow.
    await page.setViewportSize({ width: 640, height: 900 });

    await emitActivityLock(page, sessionId);

    const overlay = page.getByRole('status').filter({ hasText: 'frs/prod 1.0.106' });
    await expect(overlay).toBeVisible();
    await expect(overlay).toContainText('Waiting for deploy to complete');

    const viewportWidth = page.viewportSize()?.width ?? 0;
    const box = await overlay.boundingBox();
    expect(box).not.toBeNull();
    expect(box?.x ?? -1).toBeGreaterThanOrEqual(0);
    // Allow a sub-pixel rounding slack; the old bug pushed the right edge tens
    // of pixels past the viewport, so this cleanly separates fixed from broken.
    expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(viewportWidth + 1);

    // And the pane it lives in must not overflow the viewport either (the root
    // cause: a pane wider than the visible area took the overlay off-screen).
    const paneRight = await page.evaluate(() => {
      const pane = document.querySelector('#erun-terminal-pane');
      return pane ? pane.getBoundingClientRect().right : Number.POSITIVE_INFINITY;
    });
    expect(paneRight).toBeLessThanOrEqual(viewportWidth + 1);

    // Restore the config default viewport for later specs in the singleton backend.
    await page.setViewportSize({ width: 1440, height: 1200 });
  });
});

interface InvokeCall {
  method: string;
  args: unknown[];
}

function parseInvoke(req: Request): InvokeCall | null {
  if (req.method() !== 'POST' || !req.url().endsWith('/__erun_invoke')) {
    return null;
  }
  let body: { method?: string; args?: unknown[] } | null = null;
  try {
    body = req.postDataJSON() as { method?: string; args?: unknown[] } | null;
  } catch {
    return null;
  }
  return body?.method ? { method: body.method, args: body.args ?? [] } : null;
}

// The selected session id isn't exposed to the frontend, so provoke a resize
// (sidebar toggle) and sniff it off the ResizeSession invoke.
async function discoverSelectedSessionId(app: AppShell, page: Page): Promise<number> {
  const waitForResize = page
    .waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession')
    .catch(() => null);
  await app.titlebar.toggleButton().click();
  const resize = await waitForResize;
  await app.titlebar.toggleButton().click();
  const id = resize ? parseInvoke(resize)?.args[0] : undefined;
  return typeof id === 'number' ? id : 0;
}

// Mirrors the activity:lock event the Go-side lockTerminalsForActivity emits;
// keep the staged payload in sync with that emitter.
async function emitActivityLock(page: Page, sessionId: number): Promise<void> {
  await page.evaluate((sid) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('activity:lock', {
      sessionId: sid,
      tenant: 'frs',
      environment: 'prod',
      locked: true,
      deployId: 'dep-1',
      reason: 'Waiting for deploy to complete',
      deployTarget: 'frs/prod 1.0.106',
    });
  }, sessionId);
}
