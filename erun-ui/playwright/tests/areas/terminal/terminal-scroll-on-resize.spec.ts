import { test, expect } from '../../../fixtures/erunApp.js';
import type { Page } from '@playwright/test';
import { parseInvoke } from '../../../pages/index.js';

// A resize must re-anchor an at-bottom viewport but not yank a reader parked in
// scrollback. A layout-panel toggle stands in for an OS window resize.
const STAGING_BUDGET_MS = 30_000;
const RESIZE_BUDGET_MS = 60_000;
// Must match the number of resizeSettled calls in the scenario below.
const RESIZE_BOUNDS_PER_SCENARIO = 4;
const SCENARIO_MARGIN_MS = 30_000;

// The scenario budget covers every inner step's budget, so no step can outlive it.
const SCENARIO_BUDGET_MS =
  STAGING_BUDGET_MS + RESIZE_BOUNDS_PER_SCENARIO * RESIZE_BUDGET_MS + SCENARIO_MARGIN_MS;

// Waiting on the last staged line, not merely some scrollback, ensures the whole
// payload has landed before measuring.
const STAGING_SENTINEL = 'scrollback sentinel';

// Taller than any viewport and wider than any cols, so a resize rewraps it.
const STAGING_PAYLOAD =
  Array.from({ length: 300 }, (_, i) => `scrollback line ${i + 1} ${'x'.repeat(220)}`).join(
    '\r\n',
  ) + `\r\n${STAGING_SENTINEL}\r\n`;

// Must outlast expect's 10s default plus the poll's head start (the emit is
// fire-and-forget), while staying inside STAGING_BUDGET_MS.
const EMIT_HOLD_MS = 12_000;

test.describe('terminal scroll on resize (#465)', () => {
  test('panel toggle re-anchors an at-bottom viewport and preserves a scrolled-up one', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SCENARIO_BUDGET_MS);

    // A per-test env keeps staged scrollback out of the shared baseline.
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible' });
    await localTab.click();

    const sessionId = await app.terminalPane.selectedSessionId();
    expect(
      sessionId,
      'the sidebar toggle issued no ResizeSession, so the session to stage into cannot be named',
    ).toBeGreaterThan(0);

    await app.terminalPane.emitOutput(sessionId, STAGING_PAYLOAD);
    await expect
      .poll(() => renderedRowText(page), { timeout: STAGING_BUDGET_MS })
      .toContain(STAGING_SENTINEL);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    const colsBefore = await readTerminalCols(page);
    expect(colsBefore).toBeGreaterThan(0);
    await resizeSettled(page, () => app.titlebar.toggleReviewPanel());
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsBefore);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    await setViewportScrollTop(page, 0);
    await expect.poll(() => terminalAtBottom(page)).toBe(false);
    const colsMid = await readTerminalCols(page);
    await watchViewportAnchor(page);
    await resizeSettled(page, () => app.titlebar.toggleReviewPanel());
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsMid);
    expect(await stopWatchingViewportAnchor(page)).toBe(false);

    await setViewportScrollTop(page, Number.MAX_SAFE_INTEGER);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);
    const colsWide = await readTerminalCols(page);
    await resizeSettled(page, () => page.setViewportSize({ width: 1080, height: 860 }));
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsWide);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    await setViewportScrollTop(page, 0);
    await expect.poll(() => terminalAtBottom(page)).toBe(false);
    const colsNarrow = await readTerminalCols(page);
    await watchViewportAnchor(page);
    await resizeSettled(page, () => page.setViewportSize({ width: 1440, height: 1200 }));
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsNarrow);
    expect(await stopWatchingViewportAnchor(page)).toBe(false);

    // Leave the viewport at the prompt for later specs on the shared backend.
    await setViewportScrollTop(page, Number.MAX_SAFE_INTEGER);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);
  });

  // Holding the /__erun_emit POST holds the render, putting the staging step past
  // expect's 10s default but inside STAGING_BUDGET_MS.
  test('staging converges when the emit carrying it is held past the old bound', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SCENARIO_BUDGET_MS);
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible' });
    await localTab.click();

    const sessionId = await app.terminalPane.selectedSessionId();
    expect(
      sessionId,
      'the sidebar toggle issued no ResizeSession, so the session to stage into cannot be named',
    ).toBeGreaterThan(0);

    // Routed after the toggle so the hold covers only the staging emit.
    await page.route('**/__erun_emit', async (route) => {
      if ((route.request().postData() ?? '').includes('terminal-output')) {
        // The hold is the stimulus (see EMIT_HOLD_MS).
        await new Promise<void>((resolve) => setTimeout(resolve, EMIT_HOLD_MS));
      }
      await route.continue();
    });

    await app.terminalPane.emitOutput(sessionId, STAGING_PAYLOAD);
    await expect
      .poll(() => renderedRowText(page), { timeout: STAGING_BUDGET_MS })
      .toContain(STAGING_SENTINEL);

    // Leave the viewport at the prompt for later specs on the shared backend.
    await setViewportScrollTop(page, Number.MAX_SAFE_INTEGER);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);
  });
});

// ResizeSession follows the refit, so it signals the resize ran; the reflow
// lands in the next two frames, which this also waits out.
async function resizeSettled(page: Page, change: () => Promise<void>): Promise<void> {
  const resized = page.waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession', {
    timeout: RESIZE_BUDGET_MS,
  });
  await change();
  await resized;
  await page.evaluate(async () => {
    for (let frame = 0; frame < 2; frame++) {
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
    }
  });
}

// The faulty force-scroll is asynchronous, so the anchor is sampled every frame
// across the whole resize.
async function watchViewportAnchor(page: Page): Promise<void> {
  await page.evaluate(() => {
    interface AnchorWatch {
      anchored: boolean;
      watching: boolean;
    }
    const state: AnchorWatch = { anchored: false, watching: true };
    (window as unknown as { __anchorWatch?: AnchorWatch }).__anchorWatch = state;
    const viewport = document.querySelector<HTMLElement>('.xterm-viewport');
    const sample = (): void => {
      if (!state.watching || !viewport) {
        return;
      }
      const maxScrollTop = viewport.scrollHeight - viewport.clientHeight;
      if (viewport.scrollTop >= maxScrollTop - 2) {
        state.anchored = true;
      }
      requestAnimationFrame(sample);
    };
    requestAnimationFrame(sample);
  });
}

async function stopWatchingViewportAnchor(page: Page): Promise<boolean> {
  return await page.evaluate(() => {
    const state = (
      window as unknown as { __anchorWatch?: { anchored: boolean; watching: boolean } }
    ).__anchorWatch;
    if (!state) {
      return false;
    }
    state.watching = false;
    return state.anchored;
  });
}

async function terminalAtBottom(page: Page): Promise<boolean> {
  return await page.evaluate(() => {
    const viewport = document.querySelector<HTMLElement>('.xterm-viewport');
    if (!viewport) {
      return false;
    }
    const maxScrollTop = viewport.scrollHeight - viewport.clientHeight;
    return viewport.scrollTop >= maxScrollTop - 2;
  });
}

async function renderedRowText(page: Page): Promise<string> {
  return await page.evaluate(() =>
    Array.from(document.querySelectorAll<HTMLElement>('.xterm-rows > div'))
      .map((row) => row.textContent ?? '')
      .join('\n'),
  );
}

// xterm ignores one scroll event after its own scrollTop write; wait two frames
// so this write's event is not coalesced with that one and discarded.
async function setViewportScrollTop(page: Page, top: number): Promise<void> {
  await page.evaluate(async (value) => {
    const viewport = document.querySelector<HTMLElement>('.xterm-viewport');
    if (!viewport) {
      return;
    }
    await new Promise<void>((resolve) => {
      requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
    });
    const before = viewport.scrollTop;
    const settled = new Promise<void>((resolve) => {
      viewport.addEventListener('scroll', () => resolve(), { once: true });
    });
    viewport.scrollTop = value;
    if (viewport.scrollTop === before) {
      // Same value, so no scroll event will fire.
      return;
    }
    await settled;
  }, top);
}

async function readTerminalCols(page: Page): Promise<number> {
  return await page.evaluate(() => {
    const el = document.querySelector<HTMLElement>('.terminal');
    const raw = el?.dataset.terminalCols ?? '';
    return raw ? Number.parseInt(raw, 10) : 0;
  });
}
