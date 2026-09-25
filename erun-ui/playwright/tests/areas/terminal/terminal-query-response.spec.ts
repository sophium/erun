import type { Page, Request } from '@playwright/test';

import type { AppShell } from '../../../pages/index.js';
import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// Guards against a terminal-query reply being misrouted to the wrong PTY: the
// pre-fix code addressed the reply to whichever session was selected at reply
// time, so switching sessions during a deferred xterm parse answered the wrong
// PTY. The fix answers the session whose output xterm is parsing right now.
//
// CPR (`ESC [ ? 6 n`) is the trigger because it is the one query still answered
// post-fix — DA1/DA2, OSC 10/11/12, and DECRQSS are now suppressed, since their
// late replies landed as junk at the bash prompt once the asking tool had exited
// or the query arrived on reattach. Its `?` prefix also dodges the display strip,
// so the bytes survive into xterm and into the per-session saved buffer.
//
// Replay invariant (third test): re-rendering a tab replays the saved buffer,
// re-parsing every query a tool ever emitted; re-answering those injects junk
// into a live PTY where nothing is waiting. A replayed query must never be
// re-answered, while a live one still must be.
//
// Harness limitation: the exact cross-session race cannot be staged
// deterministically here — the Redux store / selected session is not exposed to
// the page, and forcing xterm to defer a parse depends on its internal timing.
// These specs lock the two observable invariants that bound the bug (replies
// route to the asking session; a non-selected session is never answered); the
// write-time capture that closes the race lives in TerminalWriteSourceQueue.ts.

const CPR_QUERY = '\x1b[?6n';

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

function isCprReply(data: unknown): data is string {
  return typeof data === 'string' && data.startsWith('\x1b[') && data.endsWith('R');
}

function cprRepliesTo(invokes: InvokeCall[], sessionId: number): string[] {
  return invokes
    .filter((call) => call.method === 'SendSessionInput' && call.args[0] === sessionId)
    .map((call) => call.args[1])
    .filter(isCprReply);
}

// btoa is safe only because every byte emitted here is < 256; a multi-byte
// payload would throw.
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

// Toggling the sidebar provokes a terminal resize, and ResizeSession carries the
// selected session id only when one is open — so this reveals the selected id, or
// 0 when nothing is open (a valid post-boot state, not an error).
async function discoverSelectedSessionId(app: AppShell, page: Page): Promise<number> {
  const waitForResize = page
    .waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession')
    .catch(() => null);
  await app.titlebar.toggleButton().click();
  const resize = await waitForResize;
  const id = resize ? parseInvoke(resize)?.args[0] : undefined;
  return typeof id === 'number' ? id : 0;
}

// The extra-terminal spawn the case below holds is the contention it exists to
// reproduce, so it is sized on the clock the line it guards used to carry: the
// extra-tab poll took its own 15s while this test's budget is 30s, so a spawn
// landing after 15s failed a test with half its clock still unspent. 17s clears
// that cap by the same 2s margin the resize spec's held emit clears its 10s, and
// lands well inside the budget below.
const SPAWN_HOLD_MS = 17_000;
// The boot, the waits for the env's default tabs, and the tab-count teardown
// around that hold, which run on the suite's ordinary clocks.
const SPAWN_HOLD_MARGIN_MS = 60_000;
// The case spends SPAWN_HOLD_MS on one deliberate stall, so its own clock has to
// cover that stall rather than expire inside it.
const SLOW_SPAWN_BUDGET_MS = SPAWN_HOLD_MS + SPAWN_HOLD_MARGIN_MS;

test.describe('terminal query responses (#347)', () => {
  test('a query in the selected session is answered to that same session', async ({
    app,
    page,
  }) => {
    const invokes = captureInvokes(page);
    const selectedId = await discoverSelectedSessionId(app, page);

    await emitTerminalOutput(page, selectedId, CPR_QUERY);

    // The reply is a round trip through the event pipeline, and the value
    // being polled belongs to this spec's own invoke capture -- so it is
    // bounded by the budget this test declared, not by expect.timeout's
    // separate 10s, which would red a 30s test at a third of its clock.
    await expect
      .poll(() => {
        const reply = invokes.find(
          (call) => call.method === 'SendSessionInput' && isCprReply(call.args[1]),
        );
        return reply?.args[0];
      }, withTestBudget())
      .toBe(selectedId);
  });

  test('a query for a non-selected session is never answered', async ({ app, page }) => {
    const invokes = captureInvokes(page);
    const selectedId = await discoverSelectedSessionId(app, page);
    // A session id that is definitely neither selected nor a live backend
    // session, so the only way it could receive a reply is the cross-session
    // misroute.
    const backgroundId = selectedId + 987_654;

    // Emit the background query first, then a foreground query. Waiting for the
    // foreground reply proves the event pipeline drained past the background
    // emit, so a missing background reply is a real negative, not just slowness.
    await emitTerminalOutput(page, backgroundId, CPR_QUERY);
    await emitTerminalOutput(page, selectedId, CPR_QUERY);

    // Same clock as the poll above: the foreground reply is a real round trip
    // whose landing this test's 30s budget already covers, so capping it at
    // expect.timeout's 10s is what turned a slow gate into a red spec.
    await expect
      .poll(
        () =>
          invokes.some(
            (call) =>
              call.method === 'SendSessionInput' &&
              isCprReply(call.args[1]) &&
              call.args[0] === selectedId,
          ),
        withTestBudget(),
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
    const localTab = app.tabStrip.tab('Local');
    await app.tabStrip.waitForTab('Local');
    // The env also spawns ERun and AI sessions, slower than Local (see
    // tab-strip.spec). Each spawn completing reassigns the active terminal
    // session; on a loaded host one can land AFTER the extra terminal below is
    // selected, so terminal-output for the extra session would be dropped (the
    // controller only writes to xterm when the payload's session IS the active
    // one) and the query never answered. Wait for both to appear first so the
    // active session settles before the extra terminal is created and selected.
    // These converge through the strip's own wait, which carries no cap of its
    // own; the 15s each used to carry sits below this test's 30s budget.
    await app.tabStrip.waitForTab('ERun');
    await app.tabStrip.waitForTab('AI');

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    // Converge on the tab reaching the strip rather than polling the extras'
    // count under a cap of its own: the strip's own wait carries no cap and
    // defers to this test's budget, where the 15s this poll carried sat below it
    // and failed a merely slow spawn with the rest of the clock unspent.
    await extraTabs.nth(initialExtraCount).waitFor({ state: 'visible' });
    const extraTab = extraTabs.last();
    await extraTab.click();

    const invokes = captureInvokes(page);
    const extraId = await discoverSelectedSessionId(app, page);
    expect(extraId).toBeGreaterThan(0);

    // 1. A live DEC query is answered once; its bytes now sit in the saved
    //    buffer, which sets up the replay below.
    await emitTerminalOutput(page, extraId, CPR_QUERY);
    await expect.poll(() => cprRepliesTo(invokes, extraId).length).toBe(1);

    // 2. Switch away and back: this replays the saved buffer — stale query
    //    included — into xterm, which is what could wrongly re-answer it.
    await localTab.click();
    await extraTab.click();

    // 3. Drain with a plain CSI query (`ESC [ 6 n`), split across two events so
    //    the per-chunk display strip cannot eat it. Its reply lacks the `?`, so
    //    it is distinguishable from a reply to the replayed DEC query; xterm
    //    parses FIFO, so the plain reply landing proves every replayed chunk
    //    has been parsed.
    await emitTerminalOutput(page, extraId, '\x1b[');
    await emitTerminalOutput(page, extraId, '6n');
    await expect
      .poll(() => cprRepliesTo(invokes, extraId).filter((r) => !r.startsWith('\x1b[?')).length)
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

  // The extra-tab wait is bounded by the test's own budget, not by a cap of its
  // own: a fixed cap inside that budget fails a spawn that is merely slow with
  // the rest of the clock unspent, which is what the removed 15s poll did. Only
  // a spawn landing after the old cap and inside the budget tells the two apart.
  test('an extra terminal whose spawn outlasts the old poll cap still converges', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SLOW_SPAWN_BUDGET_MS);
    // A per-test seeded env keeps this case's extra-terminal churn out of the
    // shared baseline rows.
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    await app.tabStrip.waitForTab('Local');
    // The env's other default tabs land asynchronously. Converging on them
    // first leaves the held route below covering the extra terminal's spawn and
    // nothing else.
    await app.tabStrip.waitForTab('ERun');
    await app.tabStrip.waitForTab('AI');

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (body.method === 'StartSession') {
        // Deliberate stimulus, not a wait for the app: this hold *is* the
        // contention the case exists to reproduce, so it is sized on the clock
        // it has to disagree with (see SPAWN_HOLD_MS).
        await new Promise<void>((resolve) => setTimeout(resolve, SPAWN_HOLD_MS));
      }
      await route.continue();
    });

    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await extraTabs.nth(initialExtraCount).waitFor({ state: 'visible' });

    // Close the spawned terminal so the extra session does not drift the
    // session set the singleton headless backend hands to later specs.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect.poll(() => extraTabs.count()).toBe(initialExtraCount);
  });
});
