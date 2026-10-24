import type { Page } from '@playwright/test';

import { expect, test, type SeededEnvironment } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';

// Switch-back restores a snapshot of the rendered screen in one write instead
// of re-feeding the retained log. A clock cannot tell the two apart (xterm
// coalesces writes), so the pane's published write count is asserted instead.

// Long enough to tell replay from snapshot, well under MAX_RETAINED_BYTES so
// the spec stays inside its timeout.
const BULK_CHUNKS = 400;
const BULK_LINE = 'x'.repeat(400);

// The control is the same click with almost no history, so machine speed
// cancels out of the ratio.
const SWITCH_CONTROL_MARKER = 'erun-switch-timing-control';
const SWITCH_BUDGET_TOLERANCE = 6;
// Each convergence step gets an explicit budget; the scenario clock is their
// sum plus margin. The drain gets the most because it scales with the log.
const DRAIN_BUDGET_MS = 60_000;
const SWITCH_STEP_BUDGET_MS = 20_000;
// Must match the number of SWITCH_STEP_BUDGET_MS-bounded steps in measureSwitchTiming.
const SWITCH_BOUNDS_PER_SCENARIO = 6;
const SCENARIO_MARGIN_MS = 30_000;
const SCENARIO_BUDGET_MS =
  DRAIN_BUDGET_MS + SWITCH_BOUNDS_PER_SCENARIO * SWITCH_STEP_BUDGET_MS + SCENARIO_MARGIN_MS;
// Must outlast expect's default 10s while staying inside DRAIN_BUDGET_MS.
const EMIT_HOLD_MS = 12_000;
// A ratio against a millisecond-wide control is noise, hence this floor.
const SWITCH_BUDGET_FLOOR_MS = 1_000;

async function emitBulkOutput(app: AppShell, sessionId: number, marker: string): Promise<void> {
  // One round trip for all chunks; per-chunk evaluate costs ~12s of IPC.
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

// Both paths land the same screen, so only the write count separates them. A
// missing attribute reads as a replay with -1 writes, i.e. a failure.
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

// Output buffered while away is replayed by design, so it is drained first
// (untimed); the measured switch then must be answered from the snapshot.
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

  // Control: same click on the same tab with a single chunk of history.
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

  await localTab.click();

  const marker = 'erun-switch-timing-marker';
  await emitBulkOutput(app, extraSessionId, marker);

  // Drain the replay; not timed.
  await extraTab.click();
  await expect(app.terminalPane.rows()).toContainText(marker, { timeout: DRAIN_BUDGET_MS });

  // Nothing arrived since the snapshot, so this must restore, not re-feed.
  await localTab.click();
  const start = Date.now();
  await extraTab.click();
  await expect(app.terminalPane.rows()).toContainText(marker, { timeout: SWITCH_STEP_BUDGET_MS });
  const switchMs = Date.now() - start;

  await expect.poll(() => terminalAtBottom(page), { timeout: SWITCH_STEP_BUDGET_MS }).toBe(true);

  // Read before teardown, which activates another session and republishes it.
  const activation = await sessionActivation(page);

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
    // The source catches a controller that stopped snapshotting; the count catches
    // one that snapshots but still replays the whole delta.
    expect(timing.activation.source).toBe('snapshot');
    expect(timing.activation.writes).toBeLessThan(BULK_CHUNKS);
  });

  // Holding the /__erun_emit POST holds the render, reproducing contention that
  // outlasts expect's default 10s clock.
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

    // Registered after setup so the hold covers only the bulk write.
    await page.route('**/__erun_emit', async (route) => {
      if ((route.request().postData() ?? '').includes('terminal-output')) {
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
    // A drain returning inside the hold did not wait for the render.
    expect(drainMs).toBeGreaterThan(EMIT_HOLD_MS / 2);

    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect(extraTabs).toHaveCount(initialExtraCount, { timeout: SWITCH_STEP_BUDGET_MS });
  });
});
