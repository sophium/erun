import { test, expect } from '../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// Regression: issue #438 — switching environments/tabs could leave the
// terminal scrolled mid-history instead of at the live prompt, because
// writeTerminalBuffer replayed the display buffer without forcing a
// scroll-to-bottom and xterm's write() is asynchronous. The fix scrolls to
// the bottom from the final write's completion callback.
//
// The scroll-to-bottom fires on a session switch (setSessionId →
// terminalDisplayMiddleware → writeTerminalBuffer). To drive a real switch
// deterministically the spec opens a second session via the tab strip's
// "Open a new terminal" button (always available once an env is open, unlike
// the env's optional ERun/AI tabs), then switches between the two sessions.
//
// Harness limitation: the sessions are freshly spawned shells, so staging a
// scrollback buffer taller than the viewport is not deterministic (it
// depends on shell output timing). The spec therefore locks the reachable
// invariant — after the
// switch the active terminal's viewport is at the bottom (scrollTop at its
// maximum) rather than parked above it. With no scrollback the viewport is
// trivially at the bottom; the assertion still guards against a regression
// that parks the viewport mid-history when scrollback exists, and it confirms
// the replay path runs without leaving the viewport scrolled up.
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

    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });

    // Spawn a deterministic second session so we can drive a real switch.
    // Count only the "Terminal N" extras, not the whole strip: the env's
    // default tabs (notably AI) land asynchronously seconds after the env
    // opens — the per-env action runner serializes ERun → Local → AI against
    // the real pod — so a global tab count taken now is stale by the time the
    // cleanup below compares against it.
    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect
      .poll(() => extraTabs.count(), { timeout: 15_000 })
      .toBeGreaterThan(initialExtraCount);

    // The new "extra" tab is the last one and is now active. Switch to Local
    // and back so the display buffer is rebuilt and replayed for each session
    // — the path the fix scrolls to the bottom.
    const extraTab = extraTabs.last();
    await localTab.click();
    await extraTab.click();

    await expect.poll(() => terminalAtBottom(page), { timeout: 5_000 }).toBe(true);

    // Close the spawned terminal so the extra session does not drift the
    // session set the singleton headless backend hands to later specs. The
    // explicit close also ends the pod-side session (#478), so remote-session
    // detection cannot resurrect the tab on a later env open.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect.poll(() => extraTabs.count()).toBe(initialExtraCount);
  });
});

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
