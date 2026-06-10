import { test, expect } from '../fixtures/erunApp.js';
import type { Page, Request } from '@playwright/test';
import type { AppShell } from '../pages/index.js';

// Regression: issue #465 — resizing the terminal (OS window resize, layout
// panel toggles, debug-pane drag) refit xterm but never re-anchored the
// viewport, so a user sitting at the live prompt was left scrolled up
// mid-history after the reflow. runTerminalResize now captures whether the
// viewport was at the bottom before fit() and scrolls back to the bottom
// afterwards — and only then, so a user reading scrollback is not yanked
// down (Nielsen #3, user control).
//
// The harness-reachable resize trigger is a layout panel toggle
// (flushTerminalResize → runTerminalResize). A real OS window resize funnels
// into the same runTerminalResize via the window-resize/ResizeObserver
// debounce (queueTerminalResize), so the toggle invariant locks the shared
// re-anchor path; the OS gesture itself is not reachable headless. The
// scrollback is staged deterministically by injecting `terminal-output`
// events for the selected session, mirroring what the Go PTY stream emits
// (same pattern as terminal-query-response.spec.ts).
test.describe('terminal scroll on resize (#465)', () => {
  test('panel toggle re-anchors an at-bottom viewport and preserves a scrolled-up one', async ({
    app,
    page,
  }) => {
    // Use a normal environment, never the local default (erun/local): its
    // local-shell tab set behaves differently and is not what this spec
    // drives. The Local tab gives a real selected session to inject into.
    const target = await app.sidebar.firstEnvironmentExcludingLocal();
    test.skip(target === null, 'no non-local environment in this developer harness');
    const { tenant, env } = target!;

    await app.sidebar.openEnvironment(tenant, env);
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });
    await localTab.click();

    const sessionId = await discoverSelectedSessionId(app, page);
    test.skip(sessionId === 0, 'no selected terminal session in this developer harness');

    // Stage more lines than any viewport height so real scrollback exists.
    // The lines are wider than any plausible cols so a cols-changing resize
    // rewraps them — the reflow that moves the viewport off the prompt.
    const lines =
      Array.from({ length: 300 }, (_, i) => `scrollback line ${i + 1} ${'x'.repeat(220)}`).join(
        '\r\n',
      ) + '\r\n';
    await emitTerminalOutput(page, sessionId, lines);
    await expect.poll(() => viewportHasScrollback(page), { timeout: 10_000 }).toBe(true);
    // xterm keeps an at-bottom viewport pinned while output streams, so the
    // staging leaves the viewport at the live prompt.
    await expect.poll(() => terminalAtBottom(page), { timeout: 5_000 }).toBe(true);

    // At-bottom resize: the review-panel toggle changes the terminal's
    // column count and rewraps the buffer; the viewport must come back to
    // the prompt after the reflow.
    const colsBefore = await readTerminalCols(page);
    expect(colsBefore).toBeGreaterThan(0);
    await app.titlebar.toggleReviewPanel();
    await expect.poll(() => readTerminalCols(page), { timeout: 5_000 }).not.toBe(colsBefore);
    await expect.poll(() => terminalAtBottom(page), { timeout: 5_000 }).toBe(true);

    // Scrolled-up resize: a reader parked in history must not be yanked to
    // the bottom by the next resize.
    await setViewportScrollTop(page, 0);
    await expect.poll(() => terminalAtBottom(page), { timeout: 5_000 }).toBe(false);
    const colsMid = await readTerminalCols(page);
    await app.titlebar.toggleReviewPanel(); // restores the panel to its original state
    await expect.poll(() => readTerminalCols(page), { timeout: 5_000 }).not.toBe(colsMid);
    // The faulty force-scroll would fire from the post-fit write callback
    // within milliseconds of the refit; sample the viewport over a short
    // window and require that it never lands at the bottom.
    expect(await viewportEverAtBottom(page, 600)).toBe(false);

    // Window resize (the gesture from the report): shrink the window while
    // at the bottom — the viewport must come back to the prompt — then grow
    // it back while scrolled up — the reading position must survive.
    await setViewportScrollTop(page, Number.MAX_SAFE_INTEGER);
    await expect.poll(() => terminalAtBottom(page), { timeout: 5_000 }).toBe(true);
    const colsWide = await readTerminalCols(page);
    await page.setViewportSize({ width: 1080, height: 860 });
    await expect.poll(() => readTerminalCols(page), { timeout: 5_000 }).not.toBe(colsWide);
    await expect.poll(() => terminalAtBottom(page), { timeout: 5_000 }).toBe(true);

    await setViewportScrollTop(page, 0);
    await expect.poll(() => terminalAtBottom(page), { timeout: 5_000 }).toBe(false);
    const colsNarrow = await readTerminalCols(page);
    await page.setViewportSize({ width: 1440, height: 1200 }); // config default
    await expect.poll(() => readTerminalCols(page), { timeout: 5_000 }).not.toBe(colsNarrow);
    expect(await viewportEverAtBottom(page, 600)).toBe(false);

    // Leave the viewport at the prompt so later specs in the singleton
    // backend see the usual at-bottom baseline.
    await setViewportScrollTop(page, Number.MAX_SAFE_INTEGER);
    await expect.poll(() => terminalAtBottom(page), { timeout: 5_000 }).toBe(true);
  });
});

interface InvokeCall {
  method: string;
  args: unknown[];
}

// parseInvoke decodes a /__erun_invoke POST body into {method, args}, or null
// when the request is not an invoke (or carries no JSON body).
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

// discoverSelectedSessionId finds the session the terminal is rendering by
// provoking a resize (sidebar toggle) and sniffing the ResizeSession invoke,
// then restores the sidebar. 0 means no session is selected.
async function discoverSelectedSessionId(app: AppShell, page: Page): Promise<number> {
  const waitForResize = page
    .waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession', { timeout: 1500 })
    .catch(() => null);
  await app.titlebar.toggleButton().click();
  const resize = await waitForResize;
  await app.titlebar.toggleButton().click();
  const id = resize ? parseInvoke(resize)?.args[0] : undefined;
  return typeof id === 'number' ? id : 0;
}

// emitTerminalOutput injects a `terminal-output` event for sessionId carrying
// raw bytes, mirroring what the Go PTY stream emits. data is base64 like the
// real payload; btoa is safe because every byte here is < 256.
async function emitTerminalOutput(page: Page, sessionId: number, raw: string): Promise<void> {
  await page.evaluate(
    (payload) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('terminal-output', {
        sessionId: payload.sessionId,
        data: btoa(payload.raw),
      });
    },
    { sessionId, raw },
  );
}

// terminalAtBottom reads the active xterm viewport's scroll position from the
// DOM renderer. The viewport is "at the bottom" when scrollTop has reached its
// maximum (scrollHeight - clientHeight), within a small rounding tolerance.
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

// viewportHasScrollback reports whether the buffer outgrew the viewport, i.e.
// there is real history to scroll through.
async function viewportHasScrollback(page: Page): Promise<boolean> {
  return await page.evaluate(() => {
    const viewport = document.querySelector<HTMLElement>('.xterm-viewport');
    return viewport !== null && viewport.scrollHeight > viewport.clientHeight + 10;
  });
}

// viewportEverAtBottom samples the viewport for `duration` ms and reports
// whether it ever reached the bottom — used to prove a scrolled-up viewport
// stays put across the asynchronous post-resize scroll window.
async function viewportEverAtBottom(page: Page, duration: number): Promise<boolean> {
  return await page.evaluate(async (ms) => {
    const viewport = document.querySelector<HTMLElement>('.xterm-viewport');
    if (!viewport) {
      return false;
    }
    const deadline = Date.now() + ms;
    while (Date.now() < deadline) {
      const maxScrollTop = viewport.scrollHeight - viewport.clientHeight;
      if (viewport.scrollTop >= maxScrollTop - 2) {
        return true;
      }
      await new Promise((resolve) => setTimeout(resolve, 25));
    }
    return false;
  }, duration);
}

// setViewportScrollTop drives a user-style scroll: assigning scrollTop fires
// the viewport's scroll event, which xterm syncs into its buffer position.
async function setViewportScrollTop(page: Page, top: number): Promise<void> {
  await page.evaluate((value) => {
    const viewport = document.querySelector<HTMLElement>('.xterm-viewport');
    if (viewport) {
      viewport.scrollTop = value;
    }
  }, top);
}

// readTerminalCols reads the column count runTerminalResize publishes onto
// the terminal root — changing cols is the proof a refit ran.
async function readTerminalCols(page: Page): Promise<number> {
  return await page.evaluate(() => {
    const el = document.querySelector<HTMLElement>('.terminal');
    const raw = el?.dataset.terminalCols ?? '';
    return raw ? Number.parseInt(raw, 10) : 0;
  });
}
