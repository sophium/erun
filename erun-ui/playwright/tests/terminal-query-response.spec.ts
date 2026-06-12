import type { Page, Request } from '@playwright/test';

import type { AppShell } from '../pages/index.js';
import { expect, test } from '../fixtures/erunApp.js';

// terminal-query-response covers issue #347: a tool inside a PTY emits a
// terminal query (CPR / DSR / DECRQSS); xterm parses it and the controller's
// registerTerminalQueryResponseHandlers wiring answers via SendSessionInput.
// The bug was that the reply was addressed to store.getState().terminal.sessionId
// read at *reply* time, so switching sessions during a deferred xterm parse
// injected the reply into the wrong PTY. The fix (TerminalWriteSourceQueue)
// tags every xterm write with its source session and answers the session whose
// output xterm is parsing right now.
//
// This spec drives the same React path the production query takes: it injects a
// `terminal-output` Wails event (which round-trips through the headless bridge's
// /__erun_emit fan-out, exactly like the real backend stream) and observes the
// resulting SendSessionInput call on /__erun_invoke.
//
// A DEC-private cursor-position request (DEC DSR, `ESC [ ? 6 n`) is the trigger:
// post-fix it is the query that both reaches xterm's parser and produces a
// reply. Its `?` prefix dodges stripTerminalResponses() in terminalBuffers.ts
// (whose patterns require a digit immediately after `ESC [`), so it is not
// stripped from the bytes written to xterm, and the `?n` handler answers it
// with a cursor-position report. DA1/DA2, OSC 10/11/12, and DECRQSS (`ESC P $ q
// … ST`) are now all suppressed — they consume the query and never reply —
// because their async reply landed at the bash prompt as junk when the asking
// tool had exited or the query arrived on reattach. Cursor position is the one
// reply still sent (tools need it; no sane default), so it is what this spec
// observes — for live parses only: issue #484 covers the replay side. Query
// bytes are saved verbatim in the per-session buffer, so re-rendering a tab
// (setSessionId → terminalDisplayMiddleware → writeTerminalBuffer) re-parses
// every query a tool ever emitted there; answering those again injects the
// reply into the live PTY where nothing is waiting, and the shell echoes it
// as typed junk (`1;64R1;69R…` at the prompt). The third test locks the
// replay invariant: a query replayed from the saved buffer is never
// re-answered, while a live query still is.
//
// Harness limitation: the exact cross-session race (xterm deferring a query's
// parse across a session switch, so reply-time selection != write-time source)
// cannot be staged deterministically here. The Redux store / selected session
// is not exposed to the page, and forcing xterm to defer a parse depends on its
// internal write-buffer timing. These tests therefore lock the two observable
// invariants that bound the bug — replies route to the asking session, and a
// query from a non-selected session is never answered — while the write-time
// capture that closes the race lives in TerminalWriteSourceQueue.ts.

const CPR_QUERY = '\x1b[?6n';

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

// captureInvokes records every window.go.main.App.<method> call the page makes.
function captureInvokes(page: Page): InvokeCall[] {
  const calls: InvokeCall[] = [];
  page.on('request', (req: Request) => {
    const call = parseInvoke(req);
    if (call) {
      calls.push(call);
    }
  });
  return calls;
}

// isCprReply matches the cursor-position report the DEC DSR handler sends back
// (cursorPositionReport returns `ESC [ ? <row> ; <col> R`).
function isCprReply(data: unknown): data is string {
  return typeof data === 'string' && data.startsWith('\x1b[') && data.endsWith('R');
}

// cprRepliesTo collects the cursor-position report strings addressed to
// sessionId, in arrival order.
function cprRepliesTo(invokes: InvokeCall[], sessionId: number): string[] {
  return invokes
    .filter((call) => call.method === 'SendSessionInput' && call.args[0] === sessionId)
    .map((call) => call.args[1])
    .filter(isCprReply);
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

// discoverSelectedSessionId finds the session the terminal is currently
// rendering. Toggling the sidebar provokes a resize; runTerminalResize calls
// ResizeSession(selected, …) only when a session is selected (id > 0). When
// nothing is open (e.g. a fresh checkout with no persisted selection), no
// ResizeSession fires and the selected id is 0 — the post-boot sentinel that
// the terminal-output gate compares against. Both outcomes are valid and
// machine-independent.
async function discoverSelectedSessionId(app: AppShell, page: Page): Promise<number> {
  const waitForResize = page
    .waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession', { timeout: 1500 })
    .catch(() => null);
  await app.titlebar.toggleButton().click();
  const resize = await waitForResize;
  const id = resize ? parseInvoke(resize)?.args[0] : undefined;
  return typeof id === 'number' ? id : 0;
}

test.describe('terminal query responses (#347)', () => {
  test('a query in the selected session is answered to that same session', async ({
    app,
    page,
  }) => {
    const invokes = captureInvokes(page);
    const selectedId = await discoverSelectedSessionId(app, page);

    await emitTerminalOutput(page, selectedId, CPR_QUERY);

    // The reply must be addressed to the session that produced the query.
    await expect
      .poll(
        () => {
          const reply = invokes.find(
            (call) => call.method === 'SendSessionInput' && isCprReply(call.args[1]),
          );
          return reply?.args[0];
        },
        { timeout: 5_000 },
      )
      .toBe(selectedId);
  });

  test('a query for a non-selected session is never answered', async ({ app, page }) => {
    const invokes = captureInvokes(page);
    const selectedId = await discoverSelectedSessionId(app, page);
    // A session id that is definitely neither selected nor a live backend
    // session, so the only way it could receive a reply is the #347 misroute.
    const backgroundId = selectedId + 987_654;

    // Emit the background query first, then a foreground query. Waiting for the
    // foreground reply proves the event pipeline drained past the background
    // emit, so a missing background reply is a real negative, not just slowness.
    await emitTerminalOutput(page, backgroundId, CPR_QUERY);
    await emitTerminalOutput(page, selectedId, CPR_QUERY);

    await expect
      .poll(
        () =>
          invokes.some(
            (call) =>
              call.method === 'SendSessionInput' &&
              isCprReply(call.args[1]) &&
              call.args[0] === selectedId,
          ),
        { timeout: 5_000 },
      )
      .toBe(true);

    const answeredToBackground = invokes.filter(
      (call) =>
        call.method === 'SendSessionInput' &&
        isCprReply(call.args[1]) &&
        call.args[0] === backgroundId,
    );
    expect(answeredToBackground).toHaveLength(0);
  });

  test('a query replayed from a saved buffer is never re-answered (#484)', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A per-test seeded env keeps this spec's extra-terminal churn out of
    // the shared baseline rows.
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });

    // Spawn a deterministic second session via the tab strip's "Open a new
    // terminal" button (same pattern as terminal-scroll-on-switch.spec.ts);
    // the new extra tab becomes the active one.
    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect
      .poll(() => extraTabs.count(), { timeout: 15_000 })
      .toBeGreaterThan(initialExtraCount);
    const extraTab = extraTabs.last();
    await extraTab.click();

    const invokes = captureInvokes(page);
    const extraId = await discoverSelectedSessionId(app, page);
    expect(extraId).toBeGreaterThan(0);

    // 1. A live DEC query is answered once — and its bytes are now part of
    //    the session's saved buffer (the `?` prefix dodges the display strip,
    //    exactly like production queries split across PTY chunks do).
    await emitTerminalOutput(page, extraId, CPR_QUERY);
    await expect.poll(() => cprRepliesTo(invokes, extraId).length, { timeout: 5_000 }).toBe(1);

    // 2. Switch away and back. The middleware rebuilds and replays the extra
    //    session's display buffer — stale query included — into xterm.
    await localTab.click();
    await extraTab.click();

    // 3. Drain with a *plain* CSI query (`ESC [ 6 n`), split across two
    //    output events so the per-chunk display strip cannot eat it. Its
    //    reply carries no `?`, so it is distinguishable from any reply to the
    //    replayed DEC query; xterm parses writes FIFO, so once the plain
    //    reply lands every replayed chunk from step 2 has been parsed.
    await emitTerminalOutput(page, extraId, '\x1b[');
    await emitTerminalOutput(page, extraId, '6n');
    await expect
      .poll(() => cprRepliesTo(invokes, extraId).filter((r) => !r.startsWith('\x1b[?')).length, {
        timeout: 5_000,
      })
      .toBe(1);

    // The replay parse is provably complete and must have contributed
    // nothing: the only DEC-shaped reply is the live one from step 1.
    const decReplies = cprRepliesTo(invokes, extraId).filter((r) => r.startsWith('\x1b[?'));
    expect(decReplies).toHaveLength(1);

    // Close the spawned terminal so the extra session does not drift the
    // session set the singleton headless backend hands to later specs.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect.poll(() => extraTabs.count()).toBe(initialExtraCount);
  });
});
