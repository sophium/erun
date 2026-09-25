import { test, expect } from '../../../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// Regression guard: a session/tab switch could leave the terminal parked
// mid-history instead of at the live prompt.
//
// Harness limitation: freshly spawned shells cannot deterministically stage
// scrollback taller than the viewport, so the spec locks only the reachable
// invariant — after a switch the active terminal's viewport sits at the bottom
// rather than parked above it.
//
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

test.describe('terminal scroll on session switch', () => {
  test('switching back to a tab lands the viewport at the bottom', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Use a per-test seeded env so the tab churn this spec drives (extra
    // terminal spawn + close) never leaks into the shared baseline rows.
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);

    const localTab = app.tabStrip.tab('Local');
    await app.tabStrip.waitForTab('Local');

    // Count only the "Terminal N" extras: the env's default tabs (notably AI)
    // land asynchronously after the env opens, so a whole-strip count taken now
    // would be stale by the time the cleanup below compares against it.
    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    // Converge on the tab reaching the strip rather than polling the extras'
    // count under a cap of its own: the strip's own wait carries no cap and
    // defers to this test's budget, where the 15s this poll carried sat below it
    // and failed a merely slow spawn with the rest of the clock unspent.
    await extraTabs.nth(initialExtraCount).waitFor({ state: 'visible' });

    // Switch away and back so each session's display buffer is rebuilt and
    // replayed — the path the fix scrolls to the bottom.
    const extraTab = extraTabs.last();
    await localTab.click();
    await extraTab.click();

    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    // Close the spawned terminal so it does not leak into the singleton
    // backend's session set (and ends the pod-side session, so remote-session
    // detection cannot resurrect the tab on a later env open).
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
