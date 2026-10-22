import type { Page } from '@playwright/test';

import { expect, test, type SeededEnvironment } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';

// Switching to a session that has produced a lot of output while it was not
// the visible tab used to re-feed its entire retained log, visibly scrolling
// through it before landing at the live prompt -- and the cost grew with the
// session's total history, unbounded for an alt-screen session.
// TerminalController now snapshots a session's rendered screen on
// switch-away (@xterm/addon-serialize) and restores it in one write on
// switch-back, replaying only the (already retention-bounded) output
// buffered since. These specs measure that directly rather than eyeballing a
// scroll animation.
//
// The measurement is structural, not a stopwatch, and that is what makes it
// able to fail. A wall-clock bound on the switch cannot red on a controller
// that has stopped snapshotting: xterm coalesces a burst of write() calls into
// a single parse, so re-feeding a session's whole retained log costs the same
// milliseconds and lands the same screen as the one snapshot write that
// carries those bytes. What the two paths differ in is the number of writes
// they make, which the terminal pane publishes on its own read-model
// attributes (see terminalActivationReadModel.ts) and the case below asserts
// on directly.

// Sized so the replay below is long enough to be told apart from a snapshot
// restore without pushing this spec's own body toward its 30s test timeout --
// emission, the drain, and the measured switch are all linear in this number.
// It is well short of MAX_RETAINED_BYTES (2_000_000, terminalBuffers.ts),
// which an earlier comment here claimed it exceeded; reaching that budget at
// this chunk size costs roughly ten times the runtime this spec can afford.
const BULK_CHUNKS = 400;
const BULK_LINE = 'x'.repeat(400);

// A switch is judged two ways, and only one of them is a clock.
//
// The clock is the whole click-to-settle time of the measured switch against
// the same click on the same tab with almost no history behind it, so a slow
// machine slows both and cancels out of the ratio. It used to be a flat 5s,
// and a flat budget is a stopwatch on the machine rather than on the product:
// a contended gate blew through it while the app was doing exactly what it is
// meant to do, and the run reported a switch-timing regression that had not
// happened. Both switches carry a fixed cost of their own -- the click, the
// pane re-attach, the poll that observes the landing -- and the control is
// that same cost on the same machine.
//
// The structural half is what this spec is actually about, and it is not a
// clock at all: the measured switch has to restore a captured screen in one
// write rather than re-feed the log behind it, which the pane reports on its
// own attributes. See the header for why no ratio can stand in for it.
const SWITCH_CONTROL_MARKER = 'erun-switch-timing-control';
const SWITCH_BUDGET_TOLERANCE = 6;
// The convergence steps the scenario hinges on -- the control line, the drain
// that re-feeds the whole retained log, the measured switch that has to restore
// it, the at-bottom poll, and the tab-count teardown -- each take an explicit
// budget rather than the 10s `expect` clock nested inside the scenario, and the
// scenario's own clock is their sum plus a margin for the work between them.
//
// Both halves are one defect seen from opposite ends: the drain is linear in
// BULK_CHUNKS and was genuinely still rendering when a flat 10s expired (a
// full-suite gate reddened here with most of the scenario's clock unspent, its
// own call log showing the replay advancing chunk by chunk to the moment it
// gave up), and a scenario that expires while one of its inner bounds still had
// budget left reports that same red one bound later. terminal-scroll-on-resize
// carries the identical shape for its own staging wait.
//
// The drain gets the largest share because it is the one step that scales with
// the log; nothing else here does.
const DRAIN_BUDGET_MS = 60_000;
const SWITCH_STEP_BUDGET_MS = 20_000;
// Must match the number of SWITCH_STEP_BUDGET_MS-bounded steps measureSwitchTiming
// takes -- the new-tab count poll, the control line, the control switch, the
// measured switch, the at-bottom poll, and the tab-count teardown -- or the
// scenario clock stops covering the bounds it is made of.
const SWITCH_BOUNDS_PER_SCENARIO = 6;
const SCENARIO_MARGIN_MS = 30_000;
const SCENARIO_BUDGET_MS =
  DRAIN_BUDGET_MS + SWITCH_BOUNDS_PER_SCENARIO * SWITCH_STEP_BUDGET_MS + SCENARIO_MARGIN_MS;
// How long the held-emit case below holds the bulk write. The clock it has to
// disagree with is the pre-fix convergence's: an `expect` with no explicit
// timeout takes expect's own 10s, and the step this reproduces is the drain, so
// the hold must outlast 10s. 12s covers that and still lands well inside
// DRAIN_BUDGET_MS, so the case passes there for the right reason.
const EMIT_HOLD_MS = 12_000;
// A ratio against a quantity only a few milliseconds wide is noise, so a quiet
// machine is held to this floor instead: a control switch that settles in a
// millisecond or two would otherwise hold the measured switch to that same
// couple of milliseconds, which is the poll's own resolution rather than a
// budget.
const SWITCH_BUDGET_FLOOR_MS = 1_000;

async function emitBulkOutput(app: AppShell, sessionId: number, marker: string): Promise<void> {
  // One page.evaluate for every chunk in this loop used to cost a separate CDP
  // round trip per chunk -- ~400 of them per call, harmless on an idle
  // machine but ~12s of pure IPC overhead under this suite's real per-pod
  // concurrency (#2173, #2174), enough on its own to blow the spec's global
  // test timeout before either switch-timing measurement below even starts.
  // emitOutputBatch keeps the same per-chunk event granularity (the app still
  // sees BULK_CHUNKS+1 discrete terminal-output events) in one round trip.
  const chunks = Array.from({ length: BULK_CHUNKS }, (_, i) => `${BULK_LINE} ${String(i)}\n`);
  chunks.push(`${marker}\n`);
  await app.terminalPane.emitOutputBatch(sessionId, chunks);
}

async function terminalAtBottom(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const viewport = document.querySelector<HTMLElement>('.xterm-viewport');
    if (!viewport) {
      return false;
    }
    const maxScrollTop = viewport.scrollHeight - viewport.clientHeight;
    return viewport.scrollTop >= maxScrollTop - 2;
  });
}

// What the pane's read model says the last activation did: how many content
// writes it made into the shared xterm, and which path filled the pane.
interface SessionActivation {
  writes: number;
  source: 'snapshot' | 'replay';
}

interface SwitchTiming {
  controlMs: number;
  switchMs: number;
  budgetMs: number;
  activation: SessionActivation;
}

// sessionActivation reads the pane's own record of the last session activation
// rather than inferring anything from the rendered screen: the two paths land
// the same screen, so only the write count separates them. A missing attribute
// is reported as a count of -1 and a `replay` source, so a pane that stopped
// publishing reads as a failure rather than as a zero-write restore.
async function sessionActivation(page: Page): Promise<SessionActivation> {
  return page.evaluate(() => {
    const root = document.querySelector<HTMLElement>('.terminal');
    const raw = root?.dataset.terminalActivationWrites;
    const writes = raw === undefined ? Number.NaN : Number(raw);
    return {
      writes: Number.isFinite(writes) ? writes : -1,
      source: root?.dataset.terminalActivationSource === 'snapshot' ? 'snapshot' : 'replay',
    } as const;
  });
}

// measureSwitchTiming drives the scenario once and returns the measured switch
// alongside the control it is judged against and what that switch did.
//
// The switch that carries the defect is the one made when nothing has arrived
// since the snapshot: output buffered while the tab was away is replayed by
// design (the snapshot only captures what was on screen when the tab was
// left), so a switch that still has that replay queued behind it is doing work
// this fix never claimed to remove. That replay is therefore drained first, on
// DRAIN_BUDGET_MS. The drain is staging, not a bound: it leaves the session
// holding the whole retained log, which is what makes the switch after it one
// that has to be answered from the snapshot. Its own duration is deliberately
// not measured -- a slow machine may take as long as it likes there.
async function measureSwitchTiming(
  app: AppShell,
  page: Page,
  seededEnv: SeededEnvironment,
): Promise<SwitchTiming> {
  const { tenant, environment } = seededEnv;
  await app.sidebar.openEnvironment(tenant, environment);

  const localTab = app.tabStrip.tab('Local');
  await app.tabStrip.waitForTab('Local');

  const tablist = page.getByRole('tablist', { name: 'Open terminals' });
  const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
  const initialExtraCount = await extraTabs.count();
  await page.getByRole('button', { name: 'Open a new terminal' }).click();
  await expect
    .poll(() => extraTabs.count(), { timeout: SWITCH_STEP_BUDGET_MS })
    .toBeGreaterThan(initialExtraCount);
  const extraTab = extraTabs.last();
  const extraSessionId = await app.terminalPane.selectedSessionId();
  expect(extraSessionId).toBeGreaterThan(0);

  // The control: one known line, then a switch away and back, so the history
  // behind the control switch is a single chunk. It is the same click on the
  // same tab as the measured switch below; only the history differs.
  await app.terminalPane.printOnlyLine(extraSessionId, SWITCH_CONTROL_MARKER);
  await expect(app.terminalPane.rows()).toContainText(SWITCH_CONTROL_MARKER, {
    timeout: SWITCH_STEP_BUDGET_MS,
  });
  await localTab.click();
  const controlStart = Date.now();
  await extraTab.click();
  await expect(app.terminalPane.rows()).toContainText(SWITCH_CONTROL_MARKER, {
    timeout: SWITCH_STEP_BUDGET_MS,
  });
  const controlMs = Date.now() - controlStart;

  // Switch away so the bulk output below accumulates while the tab is not the
  // one rendering live -- a background session nobody is looking at, which is
  // the case this spec is about.
  await localTab.click();

  const marker = 'erun-switch-timing-marker';
  await emitBulkOutput(app, extraSessionId, marker);

  // The drain: everything emitted while the tab was away, replayed. Waiting for
  // the marker is what leaves the session holding the whole retained log, which
  // is the state the measurement needs. How long that took is not a bound on
  // anything, so this step is not timed.
  await extraTab.click();
  await expect(app.terminalPane.rows()).toContainText(marker, { timeout: DRAIN_BUDGET_MS });

  // The measurement: the session has that whole log behind it now, and nothing
  // has arrived since the snapshot taken on the way out. Re-entering must
  // restore the captured screen, not re-feed the log.
  await localTab.click();
  const start = Date.now();
  await extraTab.click();
  await expect(app.terminalPane.rows()).toContainText(marker, { timeout: SWITCH_STEP_BUDGET_MS });
  const switchMs = Date.now() - start;

  // The landing state is the live prompt, not mid-scrollback.
  await expect.poll(() => terminalAtBottom(page), { timeout: SWITCH_STEP_BUDGET_MS }).toBe(true);

  // Read the pane's record of the switch only now that both convergence steps
  // above have landed. It is published synchronously with the click, so no
  // retry is owed here; it has to be read before the teardown below, which
  // activates another session and republishes it.
  const activation = await sessionActivation(page);

  // Clean up so the spawned terminal does not leak into the singleton
  // backend's session set.
  await tablist
    .getByRole('button', { name: /^Close / })
    .last()
    .click();
  await expect(extraTabs).toHaveCount(initialExtraCount, { timeout: SWITCH_STEP_BUDGET_MS });

  return {
    controlMs,
    switchMs,
    budgetMs: Math.max(SWITCH_BUDGET_FLOOR_MS, controlMs * SWITCH_BUDGET_TOLERANCE),
    activation,
  };
}

test.describe('terminal switch timing (#1322)', () => {
  test('switching back to a session with a large retained log lands quickly', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SCENARIO_BUDGET_MS);
    const timing = await measureSwitchTiming(app, page, seededEnv);
    expect(timing.switchMs).toBeLessThan(timing.budgetMs);
    // The switch restored a captured screen rather than re-feeding the log.
    // Two assertions, because they fail on different half-fixes: the source
    // names the path the switch took, and the count catches a switch that took
    // the snapshot path but paid for the whole log anyway. A controller that
    // stopped snapshotting reports `replay` with one write per retained chunk;
    // one that captures a snapshot but never clears the delta beside it still
    // reports `snapshot` while the count climbs with the log.
    expect(timing.activation.source).toBe('snapshot');
    expect(timing.activation.writes).toBeLessThan(BULK_CHUNKS);
  });

  // The red a full-suite gate took on the case above, forced on demand instead
  // of waited for.
  //
  // The drain is linear in the log it re-feeds, so a machine rendering that
  // replay twice as slowly as the reference is not misbehaving -- it is slower,
  // and the 10s `expect` clock the step carried gave up on a render that was
  // still progressing (that run's own call log walks the replay chunk by chunk
  // to the moment the clock expired, with most of the scenario's budget
  // unspent). The fix is not a larger number on that step but the scenario's
  // own declared clock, which the pre-fix step never consulted.
  //
  // The hold reproduces the contention the report describes at a named seam:
  // emitOutput reaches xterm through the headless shim's EventsEmit, a POST to
  // /__erun_emit that the backend re-broadcasts down the /__erun_events SSE
  // stream, so holding that POST holds the render. Fire-and-forget from
  // page.evaluate, so the drain's own clock starts before the emit lands --
  // the same arrangement the held-emit case in terminal-scroll-on-resize.spec.ts
  // uses, and sized for the same reason: just past the 10s clock it has to
  // disagree with, well inside DRAIN_BUDGET_MS so the case still passes there
  // for the right reason.
  test('the drain converges when the emit carrying it is held past the old bound', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SCENARIO_BUDGET_MS);
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);

    const localTab = app.tabStrip.tab('Local');
    await app.tabStrip.waitForTab('Local');

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect
      .poll(() => extraTabs.count(), { timeout: SWITCH_STEP_BUDGET_MS })
      .toBeGreaterThan(initialExtraCount);
    const extraTab = extraTabs.last();
    const extraSessionId = await app.terminalPane.selectedSessionId();
    expect(extraSessionId).toBeGreaterThan(0);

    // Registered after the setup above so the hold covers the bulk write and
    // nothing else; the frontend emits no events of its own, so this POST is
    // the only page-originated traffic on the route.
    await page.route('**/__erun_emit', async (route) => {
      if ((route.request().postData() ?? '').includes('terminal-output')) {
        // Deliberate stimulus, not a wait for the app: this hold *is* the
        // contention the case exists to reproduce, so it is sized on the clock
        // it has to disagree with (see EMIT_HOLD_MS).
        await new Promise<void>((resolve) => setTimeout(resolve, EMIT_HOLD_MS));
      }
      await route.continue();
    });

    await localTab.click();
    const marker = 'erun-switch-timing-held-marker';
    await emitBulkOutput(app, extraSessionId, marker);

    const drainStart = Date.now();
    await extraTab.click();
    await expect(app.terminalPane.rows()).toContainText(marker, { timeout: DRAIN_BUDGET_MS });
    const drainMs = Date.now() - drainStart;
    // The hold is the floor: the step cannot converge before the emit it is
    // waiting on has been allowed through, so a step that returned inside it
    // did not wait for the render at all.
    expect(drainMs).toBeGreaterThan(EMIT_HOLD_MS / 2);

    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect(extraTabs).toHaveCount(initialExtraCount, { timeout: SWITCH_STEP_BUDGET_MS });
  });
});
