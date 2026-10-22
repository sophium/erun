import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import type { Page, Request } from '@playwright/test';
import type { AppShell } from '../../../pages/index.js';

// Regression for #1767: FitAddon.fit() applied whatever the container
// proposed, even a container caught mid layout-change reporting a couple of
// columns (the report's trigger was the sidebar going 220px -> 280px). xterm
// rewraps scrollback at whatever width it's resized to, so that one bad fit
// permanently mangled everything already on screen. The guard (safeFit,
// erun-ui/frontend/src/app/terminalFit.ts) skips a fit whose proposal is too
// small to trust; this spec forces the pane's real FitAddon parent through
// exactly that near-zero state and proves the skip, the later recovery once
// the container is measurable again, and that scrollback survives.
//
// The exact race that produced a near-zero reading during a real sidebar drag
// is a browser-timing artifact, not something a deterministic spec should
// depend on hitting. Forcing the pane's measured size directly exercises the
// same application code path (ResizeObserver -> queueTerminalResize ->
// runTerminalResize -> safeFit) against the same shape of bad measurement,
// without relying on a race.
//
// The staged output reaches the viewport over the emit bridge, and the poll
// that reads it carries no timeout of its own: `expect.poll` with none resolves
// to expect's 10s default rather than the 30s this test declares, so output
// that is merely slow reds the step with the test's own clock unspent. The
// held-emit case at the end of this file reproduces that; `withTestBudget()`
// points the poll at the budget its test declared, where a viewport that never
// updates still fails -- now at that deadline.
test.describe('terminal fit guard against a near-zero container (#1767)', () => {
  test('a near-zero container mid-resize is skipped and the buffer survives; the next real size retries it', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible' });
    await localTab.click();

    const sessionId = await discoverSelectedSessionId(app, page);
    expect(sessionId).toBeGreaterThan(0);

    const anchor = `anchor-${'x'.repeat(120)}`;
    await emitTerminalOutput(page, sessionId, `${anchor}\r\nmore output\r\n`);
    // The poll waits on the emit bridge's own round trip, so it takes the clock
    // this test declared rather than expect's 10s default; see the held-emit
    // case at the end of this file.
    await expect.poll(() => rowsText(page), withTestBudget()).toContain(anchor);

    const colsBefore = await readTerminalCols(page);
    expect(colsBefore).toBeGreaterThan(0);

    // Force the FitAddon's own measured parent (#erun-terminal-pane) down to
    // the couple-of-columns proposal a mid-transition container reports.
    // Shrinking it also shrinks the observed `.terminal` child (w-full/h-full),
    // so the app's existing ResizeObserver fires exactly as it would for a
    // real layout change.
    const skippedResize = waitForNextResizeSession(page);
    await setTerminalPaneOverrideSize(page, { width: 20, height: 20 });
    await skippedResize;

    // Skipped, not applied: cols/rows are unchanged and the anchor line is
    // still intact at full width, not rewrapped into a column of fragments.
    expect(await readTerminalCols(page)).toBe(colsBefore);
    expect(await rowsText(page)).toContain(anchor);

    // The transition settling at its real size is itself the retry: the same
    // observer fires again, and this time the proposal is trustworthy.
    const recoveredResize = waitForNextResizeSession(page);
    await setTerminalPaneOverrideSize(page, null);
    await recoveredResize;

    expect(await readTerminalCols(page)).toBe(colsBefore);
    expect(await rowsText(page)).toContain(anchor);
  });

  // The staged output is delivered over the emit bridge -- `runtime.EventsEmit`
  // POSTs to /__erun_emit and the backend streams it back to the app -- so the
  // poll that reads the viewport is bounded by the round trip's own clock. With
  // no timeout of its own it resolves to expect's 10s default while this test
  // declares 60s, so output that is merely slow reds the step with the test's
  // own clock unspent, which is how a loaded machine turns into a failing
  // branch nobody touched.
  //
  // The emit is held past that 10s default -- the smallest delay that
  // discriminates -- so the suite pays seconds here rather than the tens a
  // genuinely loaded machine would. The hold is injected at the bridge's own
  // POST rather than by loading the machine, and the anchor is staged only
  // after the gate is armed, so the viewport provably cannot show it early:
  // pre-fix this case reds at exactly 10_000ms with 50s of its own budget
  // unused, post-fix it passes at the emit's real arrival.
  test('output that lands past the step cap still reaches the viewport', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible' });
    await localTab.click();

    const sessionId = await discoverSelectedSessionId(app, page);
    expect(sessionId).toBeGreaterThan(0);

    let releaseEmit: () => void = () => undefined;
    const emitHeld = new Promise<void>((resolve) => {
      releaseEmit = resolve;
    });
    await page.route('**/__erun_emit', async (route) => {
      await emitHeld;
      await route.continue();
    });

    const anchor = `anchor-${'x'.repeat(120)}`;
    await emitTerminalOutput(page, sessionId, `${anchor}\r\nmore output\r\n`);

    // Deliberate stimulus, not a wait for the app: the hold *is* the contention
    // this case exists to reproduce, so it is sized on the clock it has to
    // disagree with.
    setTimeout(releaseEmit, 12_000);
    await expect.poll(() => rowsText(page), withTestBudget()).toContain(anchor);
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

// The app always calls ResizeSession at the end of its resize path, whether or
// not the fit itself ran -- so it's a reliable "the debounced resize handling
// finished" signal for both the skip and the recovery below.
//
// No timeout of its own, so the wait resolves against the budget the enclosing
// test declared -- the clock it would otherwise get. The whole of what this
// step waits for is the 40ms resize debounce plus one loopback ResizeSession,
// and the 60s it used to carry sat outside the 30s test it was nested in: it
// could never fire, and it was fifteen times the duration of the entire test
// on a quiet box.
function waitForNextResizeSession(page: Page): Promise<Request> {
  return page.waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession');
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

// Mirrors what the Go PTY stream emits so the test drives the real output
// path. btoa is safe here only because every staged byte is < 256.
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

// FitAddon.proposeDimensions() reads the computed size of the `.terminal`
// element's parent (#erun-terminal-pane), not `.terminal` itself -- an inline
// style here is what a real momentarily-tiny layout would look like to it.
// `.terminal` is `w-full h-full` inside it, so this also changes what
// TerminalController's own ResizeObserver (which watches `.terminal`) sees,
// firing the real resize path exactly as a genuine layout change would.
async function setTerminalPaneOverrideSize(
  page: Page,
  size: { width: number; height: number } | null,
): Promise<void> {
  await page.evaluate((s) => {
    const pane = document.getElementById('erun-terminal-pane');
    if (!pane) {
      return;
    }
    if (s === null) {
      pane.style.removeProperty('width');
      pane.style.removeProperty('height');
    } else {
      pane.style.width = `${String(s.width)}px`;
      pane.style.height = `${String(s.height)}px`;
    }
  }, size);
}

// A changed column count is the observable proof that a refit ran.
async function readTerminalCols(page: Page): Promise<number> {
  return await page.evaluate(() => {
    const el = document.querySelector<HTMLElement>('.terminal');
    const raw = el?.dataset.terminalCols ?? '';
    return raw ? Number.parseInt(raw, 10) : 0;
  });
}

// The visible viewport's rendered text -- enough to tell a real line apart
// from the "one character per row" shape a bad rewrap produces, without
// needing to scroll: the two staged lines are recent enough to still be on
// screen.
async function rowsText(page: Page): Promise<string> {
  return await page.evaluate(
    () => document.querySelector('.xterm-rows')?.textContent?.replace(/\s+/g, '') ?? '',
  );
}
