import type { Page, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { parseInvoke } from '../../../pages/index.js';

// Opening an environment hands the terminal pane over in two steps, and the
// second one runs late. registerOpenSessionResult moves the pane onto the
// session the open just spawned; only once the environment's default tabs have
// been ensured does restoreSelectedTabForEnv put it back on the tab that
// environment remembers. Both happen inside openSelection's own await chain,
// so between them the open is parked on a spawn the operator is not waiting on
// -- and the operator's next move on the pane is invisible to it. When the
// restore then lands, it re-points the pane at a tab the operator has already
// left.
//
// That is a lost render, not a slow one. The pane renders from the store's
// active session -- TerminalController writes output only for the session the
// store names, and buffers the rest for the next activation -- so once the
// restore has moved the store off the operator's tab, that tab's output is
// dropped on arrival and the pane sits blank: nothing dispatches again, so
// nothing ever replays what was buffered.
//
// The overlap is constructed rather than waited for. The last thing the open
// awaits before the restore is the AI tab's spawn, so holding that one call
// open widens the window between the two hand-offs to a fixed size, and the
// tab the operator picks below is guaranteed to land inside it. The window is
// real and unbounded in production; this only makes it deterministic.

// How long the AI tab's spawn is held. Long enough that everything the
// operator does below lands inside the open, short enough that the spec is
// still about the hand-off rather than about waiting.
const AI_SPAWN_HOLD_MS = 8_000;
const STEP_BUDGET_MS = 20_000;
const SCENARIO_MARGIN_MS = 30_000;
const SCENARIO_BUDGET_MS = AI_SPAWN_HOLD_MS + 4 * STEP_BUDGET_MS + SCENARIO_MARGIN_MS;

const MARKER = 'erun-session-ownership-marker';

// holdAISpawn parks the environment open's last awaited spawn. Everything the
// open does before it has already happened; everything after it -- including
// the restore that takes the pane back -- is parked with it.
async function holdAISpawn(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route) => {
    const body = route.request().postDataJSON() as { method?: string } | null;
    if (body?.method === 'StartAISession') {
      // Deliberate stimulus, not a wait for the app: this hold is the window
      // the case exists to reproduce (see AI_SPAWN_HOLD_MS).
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
    // The ERun tab is recorded by registerOpenSessionResult -- the first
    // hand-off -- so once it is up the open is inside the window held below.
    await app.tabStrip.waitForTab('ERun');

    const aiSpawnSettled = page.waitForResponse(
      (response) => parseInvoke(response.request())?.method === 'StartAISession',
      { timeout: STEP_BUDGET_MS },
    );

    // The operator picks a tab of their own while the open is still running.
    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect
      .poll(() => extraTabs.count(), { timeout: STEP_BUDGET_MS })
      .toBeGreaterThan(initialExtraCount);

    const extraSessionId = await app.terminalPane.selectedSessionId();
    expect(extraSessionId).toBeGreaterThan(0);

    // Settle on the open having finished, in both directions: its last spawn
    // answered, and the tab that spawn recorded is on screen. The second is
    // the one that bounds the restore -- the strip is committed in a macrotask
    // React schedules, so a tab on screen means every promise continuation the
    // open still had pending, the restore among them, has already run. Only
    // then is what follows attributable to the hand-off rather than to a race
    // with it.
    await aiSpawnSettled;
    await app.tabStrip.waitForTab('AI');

    // Output for the tab the operator picked has to reach the pane.
    await app.terminalPane.emitOutput(extraSessionId, `${MARKER}\n`);
    await expect(app.terminalPane.rows()).toContainText(MARKER, { timeout: STEP_BUDGET_MS });

    // And the pane is still that tab's: the open's restore must not have taken
    // it back. Asserted separately, because a pane that rendered the marker
    // and was then re-pointed reads the same on the screen but is a different
    // failure -- and the next output would be the one lost.
    expect(await app.terminalPane.selectedSessionId()).toBe(extraSessionId);

    // Clean up so the spawned terminal does not leak into the singleton
    // backend's session set.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect(extraTabs).toHaveCount(initialExtraCount, { timeout: STEP_BUDGET_MS });
  });
});
