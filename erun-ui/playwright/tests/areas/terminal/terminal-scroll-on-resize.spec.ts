import { test, expect } from '../../../fixtures/erunApp.js';
import type { Page } from '@playwright/test';
import { parseInvoke } from '../../../pages/index.js';

// Regression: a terminal resize refit xterm but never re-anchored the
// viewport, leaving a user at the live prompt stranded mid-history. The fix
// re-anchors only a viewport that was already at the bottom, so a reader
// parked in scrollback is not yanked down.
//
// A real OS window resize is not reachable headless, so a layout-panel toggle
// drives the same shared re-anchor path; scrollback is staged by injecting
// terminal-output events for the selected session.
//
// Every convergence below is bounded by this budget, not by the 10s `expect`
// clock nested inside it. A 30s scenario whose staging wait expires at 10s
// reports a failure of a step that was still running, with two thirds of the
// clock it was sized against unspent -- which is the shape a full-suite gate
// reddened on, twice.
const SCENARIO_BUDGET_MS = 120_000;
// The staging wait is the longest single step here -- it covers the app parsing
// and rendering the whole staged history, the one step that scales with the
// machine -- so it gets the largest share of that budget.
const STAGING_BUDGET_MS = 30_000;

// The staged history ends with this line, and the spec waits for it to be
// rendered. A terminal that has written only the first few lines of the
// staging already has scrollback, so waiting on "some scrollback exists"
// releases the spec while the rest of the history is still streaming in --
// every measurement after it is then taken against a terminal whose size is
// still changing. The last staged line can only be on screen once the whole
// payload has landed.
const STAGING_SENTINEL = 'scrollback sentinel';

test.describe('terminal scroll on resize (#465)', () => {
  test('panel toggle re-anchors an at-bottom viewport and preserves a scrolled-up one', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SCENARIO_BUDGET_MS);

    // A per-test seeded env keeps the scrollback this spec stages from leaking
    // into the shared baseline rows.
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

    // Stage more lines than any viewport height so real scrollback exists.
    // The lines are wider than any plausible cols so a cols-changing resize
    // rewraps them — the reflow that moves the viewport off the prompt.
    const lines =
      Array.from({ length: 300 }, (_, i) => `scrollback line ${i + 1} ${'x'.repeat(220)}`).join(
        '\r\n',
      ) +
      `\r\n${STAGING_SENTINEL}\r\n`;
    await app.terminalPane.emitOutput(sessionId, lines);
    await expect
      .poll(() => renderedRowText(page), { timeout: STAGING_BUDGET_MS })
      .toContain(STAGING_SENTINEL);
    // xterm keeps an at-bottom viewport pinned while output streams, so the
    // staging leaves the viewport at the live prompt.
    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    // At-bottom resize: after the reflow the viewport must come back to the prompt.
    const colsBefore = await readTerminalCols(page);
    expect(colsBefore).toBeGreaterThan(0);
    await resizeSettled(page, () => app.titlebar.toggleReviewPanel());
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsBefore);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    // Scrolled-up resize: a reader parked in history must not be yanked to
    // the bottom by the next resize.
    await setViewportScrollTop(page, 0);
    await expect.poll(() => terminalAtBottom(page)).toBe(false);
    const colsMid = await readTerminalCols(page);
    await watchViewportAnchor(page);
    await resizeSettled(page, () => app.titlebar.toggleReviewPanel());
    expect(await stopWatchingViewportAnchor(page)).toBe(false);
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsMid);

    // Window resize (the gesture from the report): at the bottom, shrinking must
    // re-anchor to the prompt; scrolled up, growing must preserve the reading position.
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
    // config default
    await resizeSettled(page, () => page.setViewportSize({ width: 1440, height: 1200 }));
    expect(await stopWatchingViewportAnchor(page)).toBe(false);
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsNarrow);

    // Leave the viewport at the prompt so later specs in the singleton
    // backend see the usual at-bottom baseline.
    await setViewportScrollTop(page, Number.MAX_SAFE_INTEGER);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);
  });
});

// A layout change refits xterm, publishes the new geometry onto the terminal
// element, and only then pushes it to the PTY — so the ResizeSession call is the
// app's own "the refit ran" signal. Bounding each resize on that event, rather
// than on how long a poll is allowed to run, is what keeps the spec honest on a
// loaded host: the old clock-bounded poll simply expired when the toggle took
// longer than the window, reporting a re-anchor failure that had not happened.
//
// The reflow is scheduled behind that signal, so this waits out the two frames
// it lands in before returning. Callers that measure the terminal right after a
// resize are then measuring a terminal that has finished moving.
async function resizeSettled(page: Page, change: () => Promise<void>): Promise<void> {
  const resized = page.waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession', {
    timeout: 60_000,
  });
  await change();
  await resized;
  await page.evaluate(async () => {
    for (let frame = 0; frame < 2; frame++) {
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
    }
  });
}

// watchViewportAnchor / stopWatchingViewportAnchor bracket a resize with a
// frame-sampled record of whether the viewport sat at the bottom.
//
// The faulty force-scroll fires asynchronously after the refit, so the
// re-anchor has to be observed across the whole resize rather than checked
// once. It used to be sampled for a fixed 600ms after the refit, which is a
// clock on the machine rather than on the product: on a contended builder the
// refit it was meant to cover had not finished when the window closed, and the
// spec reported a yank that had not happened. Both ends of the observation are
// now app events — it opens before the toggle and closes once the refit has
// settled and rendered — so a loaded machine stretches the window with the
// work instead of expiring ahead of it.
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
    const state = (window as unknown as { __anchorWatch?: { anchored: boolean; watching: boolean } })
      .__anchorWatch;
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

// xterm only renders the viewport's own rows, so the rendered rows are the
// observable form of "this line is on screen right now".
async function renderedRowText(page: Page): Promise<string> {
  return await page.evaluate(() =>
    Array.from(document.querySelectorAll<HTMLElement>('.xterm-rows > div'))
      .map((row) => row.textContent ?? '')
      .join('\n'),
  );
}

// setViewportScrollTop drives a user-style scroll: assigning scrollTop fires
// the viewport's scroll event, which xterm syncs into its buffer position.
// xterm ignores exactly one native 'scroll' event after it writes scrollTop
// itself (e.g. the pin-to-bottom write that follows a resize's reflow), so it
// can tell its own write apart from a real user scroll. Issuing this write
// immediately after such an app-driven one risks the browser coalescing both
// into a single dispatched event — which xterm then discards as the one it
// was expecting from its own write, silently dropping this position change
// while the DOM still shows it. Waiting two animation frames first lets any
// such pending write's event fire and be consumed on its own before this one
// is issued, so the two can never be mistaken for each other.
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
      // The assignment landed on the same (possibly clamped) value already in
      // place, so no scroll event will fire — don't wait for one.
      return;
    }
    await settled;
  }, top);
}

// A changed column count is the observable proof that a refit ran.
async function readTerminalCols(page: Page): Promise<number> {
  return await page.evaluate(() => {
    const el = document.querySelector<HTMLElement>('.terminal');
    const raw = el?.dataset.terminalCols ?? '';
    return raw ? Number.parseInt(raw, 10) : 0;
  });
}
