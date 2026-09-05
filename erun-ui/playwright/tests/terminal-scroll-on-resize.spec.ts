import { test, expect } from '../fixtures/erunApp.js';
import type { Page, Request } from '@playwright/test';
import type { AppShell } from '../pages/index.js';

// Regression: a terminal resize refit xterm but never re-anchored the
// viewport, leaving a user at the live prompt stranded mid-history. The fix
// re-anchors only a viewport that was already at the bottom, so a reader
// parked in scrollback is not yanked down.
//
// A real OS window resize is not reachable headless, so a layout-panel toggle
// drives the same shared re-anchor path; scrollback is staged by injecting
// terminal-output events for the selected session.
test.describe('terminal scroll on resize (#465)', () => {
  test('panel toggle re-anchors an at-bottom viewport and preserves a scrolled-up one', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A per-test seeded env keeps the scrollback this spec stages from leaking
    // into the shared baseline rows.
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible' });
    await localTab.click();

    const sessionId = await discoverSelectedSessionId(app, page);
    expect(sessionId).toBeGreaterThan(0);

    // Stage more lines than any viewport height so real scrollback exists.
    // The lines are wider than any plausible cols so a cols-changing resize
    // rewraps them — the reflow that moves the viewport off the prompt.
    const lines =
      Array.from({ length: 300 }, (_, i) => `scrollback line ${i + 1} ${'x'.repeat(220)}`).join(
        '\r\n',
      ) + '\r\n';
    await emitTerminalOutput(page, sessionId, lines);
    await expect.poll(() => viewportHasScrollback(page)).toBe(true);
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
    await resizeSettled(page, () => app.titlebar.toggleReviewPanel());
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsMid);
    // The faulty force-scroll would fire asynchronously within milliseconds of
    // the refit, so sample over a short window rather than checking once.
    expect(await viewportEverAtBottom(page, 600)).toBe(false);

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
    // config default
    await resizeSettled(page, () => page.setViewportSize({ width: 1440, height: 1200 }));
    await expect.poll(() => readTerminalCols(page)).not.toBe(colsNarrow);
    expect(await viewportEverAtBottom(page, 600)).toBe(false);

    // Leave the viewport at the prompt so later specs in the singleton
    // backend see the usual at-bottom baseline.
    await setViewportScrollTop(page, Number.MAX_SAFE_INTEGER);
    await expect.poll(() => terminalAtBottom(page)).toBe(true);
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

// A layout change refits xterm, publishes the new geometry onto the terminal
// element, and only then pushes it to the PTY — so the ResizeSession call is the
// app's own "the refit ran" signal. Bounding each resize on that event, rather
// than on how long a poll is allowed to run, is what keeps the spec honest on a
// loaded host: the old clock-bounded poll simply expired when the toggle took
// longer than the window, reporting a re-anchor failure that had not happened.
async function resizeSettled(page: Page, change: () => Promise<void>): Promise<void> {
  const resized = page.waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession', {
    timeout: 60_000,
  });
  await change();
  await resized;
}

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

// Mirrors what the Go PTY stream emits so the test drives the real output path.
// btoa is safe here only because every staged byte is < 256.
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

async function viewportHasScrollback(page: Page): Promise<boolean> {
  return await page.evaluate(() => {
    const viewport = document.querySelector<HTMLElement>('.xterm-viewport');
    return viewport !== null && viewport.scrollHeight > viewport.clientHeight + 10;
  });
}

// Proves a scrolled-up viewport stays put across the asynchronous post-resize
// scroll window.
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
