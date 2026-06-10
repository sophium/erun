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
// observes.
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
});
