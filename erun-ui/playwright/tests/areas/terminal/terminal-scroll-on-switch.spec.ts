import { test, expect } from '../../../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// Regression guard: a session/tab switch could leave the terminal parked
// mid-history instead of at the live prompt.
//
// Harness limitation: freshly spawned shells cannot deterministically stage
// scrollback taller than the viewport, so the spec locks only the reachable
// invariant — after a switch the active terminal's viewport sits at the bottom
// rather than parked above it.
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

    // Count only the "Terminal N" extras: the env's default tabs (notably AI)
    // land asynchronously after the env opens, so a whole-strip count taken now
    // would be stale by the time the cleanup below compares against it.
    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect
      .poll(() => extraTabs.count(), { timeout: 15_000 })
      .toBeGreaterThan(initialExtraCount);

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
