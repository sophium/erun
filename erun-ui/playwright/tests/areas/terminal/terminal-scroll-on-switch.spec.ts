import { test, expect } from '../../../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// After a tab switch the active terminal's viewport must sit at the bottom.
// SPAWN_HOLD_MS is held past a 15s step cap and inside the budget.
const SPAWN_HOLD_MS = 17_000;
const SPAWN_HOLD_MARGIN_MS = 60_000;
const SLOW_SPAWN_BUDGET_MS = SPAWN_HOLD_MS + SPAWN_HOLD_MARGIN_MS;

test.describe('terminal scroll on session switch', () => {
  test('switching back to a tab lands the viewport at the bottom', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A per-test env keeps tab churn out of the shared baseline.
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);

    const localTab = app.tabStrip.tab('Local');
    await app.tabStrip.waitForTab('Local');

    // Count only "Terminal N" extras: default tabs land asynchronously.
    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await extraTabs.nth(initialExtraCount).waitFor({ state: 'visible' });

    // Switching replays each session's display buffer.
    const extraTab = extraTabs.last();
    await localTab.click();
    await extraTab.click();

    await expect.poll(() => terminalAtBottom(page)).toBe(true);

    // Close it so remote-session detection cannot resurrect it later.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect.poll(() => extraTabs.count()).toBe(initialExtraCount);
  });

  // A spawn landing after a 15s step cap but inside the budget must converge.
  test('an extra terminal whose spawn outlasts the old poll cap still converges', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SLOW_SPAWN_BUDGET_MS);
    // A per-test env keeps extra-terminal churn out of the shared baseline.
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    await app.tabStrip.waitForTab('Local');
    // Wait for the other default tabs so the held route covers only the extra spawn.
    await app.tabStrip.waitForTab('ERun');
    await app.tabStrip.waitForTab('AI');

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (body.method === 'StartSession') {
        // The hold is the stimulus (see SPAWN_HOLD_MS).
        await new Promise<void>((resolve) => setTimeout(resolve, SPAWN_HOLD_MS));
      }
      await route.continue();
    });

    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await extraTabs.nth(initialExtraCount).waitFor({ state: 'visible' });

    // Close the extra terminal so later specs see the same session set.
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
