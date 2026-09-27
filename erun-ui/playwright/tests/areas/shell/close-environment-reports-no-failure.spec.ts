import type { Page } from '@playwright/test';

import type { SeededEnvironment } from '../../../fixtures/erunApp.js';
import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';
import type { AppShell } from '../../../pages/AppShell.js';

// Closing an env tears its PTYs down on purpose: CloseEnvironmentSessions ->
// collectAndMarkClosedForSelection -> the session's own Close(), which SIGKILLs
// the process group. A reader already parked in Read wakes on that kill with a
// non-empty reason ("signal: killed"), and handleTerminalExit reported it to
// the operator — as "Failed to open <tenant> / <env>: signal: killed" on the
// env's ERun tab, whose session carries the openSelection, and as the bare
// reason on its Local and AI tabs, which carry none. Both are a close the
// operator watched happen being reported back to them as something that went
// wrong. dropExitedSessionFromTabs has always known that a close in flight owns
// its own exits; the feedback dispatch did not.
//
// Each test holds the close RPC's response open for the whole assertion
// window, so the exit the desktop is causing provably lands while the close is
// in flight — the mark is still up, the session still the selected one —
// rather than racing the tail that clears both. Without the hold the spec
// would be betting on the SSE event beating the RPC response.

// bannersAfterClose opens the seeded env, puts `focusTab` in charge of the
// pane, holds the close RPC open while the in-flight close's own exit lands,
// and returns every titlebar line the desktop rendered.
async function bannersAfterClose(
  app: AppShell,
  page: Page,
  seededEnv: SeededEnvironment,
  focusTab: string,
): Promise<string[]> {
  await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
  await app.tabStrip.waitForTab(focusTab);
  await app.tabStrip.tab(focusTab).click();

  let closeRequests = 0;
  let releaseClose!: () => void;
  const closeReleased = new Promise<void>((resolve) => {
    releaseClose = resolve;
  });
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method !== 'CloseEnvironmentSessions') {
      await route.continue();
      return;
    }
    closeRequests += 1;
    // fetch() lets the backend really run the close — killing the PTYs, which
    // is what emits the exits under test — while its response is withheld.
    const response = await route.fetch();
    await closeReleased;
    await route.fulfill({ response });
  });

  await app.titlebar.recordBanners();

  // The control sits inside a tooltip that focusing opens, so a single keypress
  // can land in the gap between focus and key. Converging on the request the
  // route handler saw turns that into a retry instead of a spec that asserted
  // against a close nothing ever dispatched.
  await expect(async () => {
    if (closeRequests === 0) {
      await app.sidebar.activateEnvCloseControl(SEED_TENANT, seededEnv.environment);
    }
    expect(closeRequests).toBeGreaterThan(0);
  }).toPass();

  // The exit event's own, fix-independent effect: the ERun tab — the exit that
  // carries an openSelection, so the one whose tab the handler takes down —
  // leaves the strip, whichever tab owns the pane. Both that removal and the
  // notice under test are dispatched in one synchronous thunk call and commit
  // together, so waiting on the tab bounds the assertion to a real event rather
  // than to a guessed delay.
  await app.tabStrip.tab('ERun').waitFor({ state: 'hidden' });

  const banners = await page.evaluate(
    () => (window as unknown as { __erunBannerLog?: string[] }).__erunBannerLog ?? [],
  );

  releaseClose();
  // The close was real, so the env stops being reported open here — the
  // assertion above would be worthless if the press had quietly done nothing.
  await expect(app.sidebar.envOpenDot(SEED_TENANT, seededEnv.environment)).toHaveCount(
    0,
    withTestBudget(),
  );
  return banners;
}

test.describe('close environment — a successful close reports no failure', () => {
  // The ERun tab is the session the failure report named: it is the one that
  // carries the env's openSelection.
  test('the PTY exit the close itself caused is not rendered as a failure', async ({
    app,
    page,
    seededEnv,
  }) => {
    const banners = await bannersAfterClose(app, page, seededEnv, 'ERun');
    expect(banners.filter((banner) => banner.includes('signal: killed'))).toEqual([]);
  });

  // The env's Local and AI tabs are killed by the same teardown and own the
  // pane just as often, but register no openSelection — so their exit reaches
  // the feedback path unwrapped, and it must be just as silent.
  test('a close reports nothing for the tab that carries no open selection', async ({
    app,
    page,
    seededEnv,
  }) => {
    const banners = await bannersAfterClose(app, page, seededEnv, 'Local');
    expect(banners.filter((banner) => banner.includes('signal: killed'))).toEqual([]);
  });
});
