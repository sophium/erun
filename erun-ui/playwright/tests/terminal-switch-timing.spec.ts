import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import type { AppShell } from '../pages/index.js';

// #1322: switching to a session that has produced a lot of output while it
// was not the visible tab used to re-feed its entire retained log, visibly
// scrolling through it before landing at the live prompt -- and the cost grew
// with the session's total history, unbounded for an alt-screen session.
// TerminalController now snapshots a session's rendered screen on
// switch-away (@xterm/addon-serialize) and restores it in one write on
// switch-back, replaying only the (already retention-bounded) output
// buffered since. These specs measure that directly rather than eyeballing a
// scroll animation.

// Comfortably above MAX_RETAINED_BYTES (2_000_000, terminalBuffers.ts) so a
// switch genuinely has to cope with more retained output than a single
// unbounded replay used to carry.
const BULK_CHUNKS = 400;
const BULK_LINE = 'x'.repeat(400);
// Generous but firm: the pre-fix symptom was ~20s for a multi-MB history
// (#1322's own report). A budget an order of magnitude tighter than that,
// left with slack for a loaded CI machine, still catches a regression back to
// O(history) switching.
const SWITCH_BUDGET_MS = 5_000;

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

test.describe('terminal switch timing (#1322)', () => {
  test('switching to a session with several MB of background output lands quickly', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);

    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect
      .poll(() => extraTabs.count(), { timeout: 15_000 })
      .toBeGreaterThan(initialExtraCount);
    const extraTab = extraTabs.last();
    const extraSessionId = await app.terminalPane.selectedSessionId();
    expect(extraSessionId).toBeGreaterThan(0);

    // Switch away so the bulk output below accumulates while the tab is not
    // the one rendering live -- exactly the case #1322 reports (a background
    // session nobody is looking at).
    await localTab.click();

    const marker = 'erun-switch-timing-marker';
    await emitBulkOutput(app, extraSessionId, marker);

    const start = Date.now();
    await extraTab.click();
    await expect(app.terminalPane.rows()).toContainText(marker, { timeout: SWITCH_BUDGET_MS });
    const elapsedFirstSwitch = Date.now() - start;
    expect(elapsedFirstSwitch).toBeLessThan(SWITCH_BUDGET_MS);

    // Repeat once more with fresh bulk output, proving the snapshot mechanism
    // pays off on every switch, not only a first, coincidentally-fast one.
    await localTab.click();
    const marker2 = 'erun-switch-timing-marker-2';
    await emitBulkOutput(app, extraSessionId, marker2);
    const start2 = Date.now();
    await extraTab.click();
    await expect(app.terminalPane.rows()).toContainText(marker2, { timeout: SWITCH_BUDGET_MS });
    const elapsedSecondSwitch = Date.now() - start2;
    expect(elapsedSecondSwitch).toBeLessThan(SWITCH_BUDGET_MS);

    // The landing state is the live prompt, not mid-scrollback.
    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    // Clean up so the spawned terminal does not leak into the singleton
    // backend's session set.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect(extraTabs).toHaveCount(initialExtraCount);
  });
});
