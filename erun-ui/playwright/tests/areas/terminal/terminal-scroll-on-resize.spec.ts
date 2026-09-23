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
// The staging wait is the longest single step here -- it covers the app parsing
// and rendering the whole staged history, the one step that scales with the
// machine -- so it gets the largest share of the budget below.
const STAGING_BUDGET_MS = 30_000;
// Every resize converges on the app's own ResizeSession request (see
// resizeSettled), bounded by this clock; the scenario spends one per resize.
const RESIZE_BUDGET_MS = 60_000;
// Must match the number of resizeSettled calls in the scenario below -- bump it
// when one is added, or the invariant in the next comment stops holding.
const RESIZE_BOUNDS_PER_SCENARIO = 4;
// The work between those bounds: toggles, scroll writes, the two frames each
// resize waits out, and the final assertions.
const SCENARIO_MARGIN_MS = 30_000;

// The convergence steps this spec re-anchored -- the staging wait and each
// resize -- take an explicit budget rather than the 10s `expect` clock nested
// inside the scenario, and the scenario's own clock is the sum of those
// budgets plus that margin. Both halves are one defect seen from opposite
// ends: a 30s scenario whose staging wait expires at 10s reports a failure of
// a step that was still running with two thirds of the clock it was sized
// against unspent, and a scenario that expires while one of its own inner
// bounds still had budget left reports exactly that red one bound later. A
// full-suite gate reddened on the first shape, twice.
const SCENARIO_BUDGET_MS =
  STAGING_BUDGET_MS + RESIZE_BOUNDS_PER_SCENARIO * RESIZE_BUDGET_MS + SCENARIO_MARGIN_MS;

// The staged history ends with this line, and the spec waits for it to be
// rendered. A terminal that has written only the first few lines of the
// staging already has scrollback, so waiting on "some scrollback exists"
// releases the spec while the rest of the history is still streaming in --
// every measurement after it is then taken against a terminal whose size is
// still changing. The last staged line can only be on screen once the whole
// payload has landed.
const STAGING_SENTINEL = 'scrollback sentinel';

// More lines than any viewport height, so staging them leaves real scrollback.
// The lines are wider than any plausible cols so a cols-changing resize
// rewraps them -- the reflow that moves the viewport off the prompt.
const STAGING_PAYLOAD =
  Array.from({ length: 300 }, (_, i) => `scrollback line ${i + 1} ${'x'.repeat(220)}`).join(
    '\r\n',
  ) + `\r\n${STAGING_SENTINEL}\r\n`;

// How long the held-emit case below holds the staging write. The clock it has
// to disagree with is the pre-fix convergence's: an `expect.poll` with no
// explicit timeout takes expect's own 10s (playwright.config.ts), and that
// deadline is a give-up point rather than a lower bound -- the poll loop breaks
// rather than sleeping past it -- so the hold must outlast 10s plus the head
// start that poll's clock gets over this one. That head start is real: the emit
// is dispatched fire-and-forget from page.evaluate, so this timer only starts
// once the route handler sees the POST, a CDP round trip after the poll's clock
// began. 2s covers it several times over on a contended builder, and lands 18s
// inside the STAGING_BUDGET_MS that replaced the 10s bound, so the case still
// passes there for the right reason. A shorter hold stops reliably disagreeing
// with the clock it exists to disagree with; a longer one only adds wall clock.
const EMIT_HOLD_MS = 12_000;

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

    await app.terminalPane.emitOutput(sessionId, STAGING_PAYLOAD);
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
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsMid);
    expect(await stopWatchingViewportAnchor(page)).toBe(false);

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
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsNarrow);
    expect(await stopWatchingViewportAnchor(page)).toBe(false);

    // Leave the viewport at the prompt so later specs in the singleton
    // backend see the usual at-bottom baseline.
    await setViewportScrollTop(page, Number.MAX_SAFE_INTEGER);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);
  });

  // The reproduction the earlier exemption on #2459 claimed was impossible:
  // "no RPC, no stub, no route gates the write". A route does, and the staging
  // write goes through it. emitOutput reaches xterm via the headless shim's
  // EventsEmit, which is a POST to /__erun_emit (headlessserver/shim.go) that
  // the backend then re-broadcasts down the /__erun_events SSE stream to the
  // app's own listener -- so holding that POST holds the render, and the
  // overlap the full-suite gate produced by contention is forced on demand here
  // instead of waited for.
  //
  // This isolates the budget half of the fix: the same staging step, payload
  // and sentinel as the case above, with the emit held past the 10s `expect`
  // clock the pre-fix convergence inherited and well inside the
  // STAGING_BUDGET_MS that replaced it.
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

    // Routed after the toggle, so the hold covers the staging emit and nothing
    // else; the frontend emits no events of its own, so this POST is the only
    // page-originated traffic on the route.
    await page.route('**/__erun_emit', async (route) => {
      if ((route.request().postData() ?? '').includes('terminal-output')) {
        // Deliberate stimulus, not a wait for the app: this hold *is* the
        // contention the case exists to reproduce, so it is sized on the clock
        // it has to disagree with (see EMIT_HOLD_MS).
        await new Promise<void>((resolve) => setTimeout(resolve, EMIT_HOLD_MS));
      }
      await route.continue();
    });

    await app.terminalPane.emitOutput(sessionId, STAGING_PAYLOAD);
    await expect
      .poll(() => renderedRowText(page), { timeout: STAGING_BUDGET_MS })
      .toContain(STAGING_SENTINEL);

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

// watchViewportAnchor / stopWatchingViewportAnchor bracket a resize with a
// frame-sampled record of whether the viewport sat at the bottom.
//
// The faulty force-scroll fires asynchronously after the refit, so the
// re-anchor has to be observed across the whole resize rather than checked
// once. It used to be sampled for a fixed 600ms after the refit, which is a
// clock on the machine rather than on the product: on a contended builder the
// refit it was meant to cover had not finished when the window closed, and the
// spec reported a yank that had not happened. Both ends of the observation are
// now app events — it opens before the toggle, and closes once the refit has
// settled, rendered, and the new geometry has converged (so the trailing edge
// is still anchored behind the same observable state the old window covered,
// not merely behind the refit that produced it) — so a loaded machine
// stretches the window with the work instead of expiring ahead of it.
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
