import type { Page, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { parseInvoke } from '../../../pages/index.js';

// An env open moves the pane to its spawned session, then later restores the
// remembered tab. A tab the operator picks in between must keep the pane, or
// its output is buffered and never rendered. Holding the AI spawn widens that
// window deterministically.

const AI_SPAWN_HOLD_MS = 8_000;
const STEP_BUDGET_MS = 20_000;
const SCENARIO_MARGIN_MS = 30_000;
const SCENARIO_BUDGET_MS = AI_SPAWN_HOLD_MS + 4 * STEP_BUDGET_MS + SCENARIO_MARGIN_MS;

const MARKER = 'erun-session-ownership-marker';

// holdAISpawn parks the open's last awaited spawn, and the restore with it.
async function holdAISpawn(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route) => {
    const body = route.request().postDataJSON() as { method?: string } | null;
    if (body?.method === 'StartAISession') {
      // The hold is the stimulus (see AI_SPAWN_HOLD_MS).
      await new Promise<void>((resolve) => setTimeout(resolve, AI_SPAWN_HOLD_MS));
    }
    await route.continue();
  });
}

test.describe('terminal session ownership (#2712)', () => {
  test('a tab picked while the environment is still opening keeps the pane', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SCENARIO_BUDGET_MS);
    const { tenant, environment } = seededEnv;

    await holdAISpawn(page);
    await app.sidebar.openEnvironment(tenant, environment);
    await app.tabStrip.waitForTab('Local');
    // ERun is recorded by the first hand-off, so the open is now inside the window.
    await app.tabStrip.waitForTab('ERun');

    const aiSpawnSettled = page.waitForResponse(
      (response) => parseInvoke(response.request())?.method === 'StartAISession',
      { timeout: STEP_BUDGET_MS },
    );

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect
      .poll(() => extraTabs.count(), { timeout: STEP_BUDGET_MS })
      .toBeGreaterThan(initialExtraCount);

    const extraSessionId = await app.terminalPane.selectedSessionId();
    expect(extraSessionId).toBeGreaterThan(0);

    // The AI tab on screen means React committed, so the restore has already run.
    await aiSpawnSettled;
    await app.tabStrip.waitForTab('AI');

    await app.terminalPane.emitOutput(extraSessionId, `${MARKER}\n`);
    await expect(app.terminalPane.rows()).toContainText(MARKER, { timeout: STEP_BUDGET_MS });

    // Checked separately: a pane re-pointed after rendering the marker would lose
    // the next output.
    expect(await app.terminalPane.selectedSessionId()).toBe(extraSessionId);

    // Close the extra terminal so it does not leak into the shared backend.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect(extraTabs).toHaveCount(initialExtraCount, { timeout: STEP_BUDGET_MS });
  });
});
