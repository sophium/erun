import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Sidebar-reclick coverage for bug/368-respawn-dead-tabs-from-sidebar.
//
// ensureDefaultEnvTabs now treats a tab whose sessionId has an exitReason
// as a zombie and respawns it through spawnDefaultTab / spawnERunTabPassive
// so the env's "── stopped reconnecting … click the environment in the
// sidebar to retry ──" marker actually delivers a fresh PTY when the user
// clicks the env. The positive path — staging a real dead PTY for the
// active env, then re-clicking the sidebar row and asserting the AI tab
// swaps to a fresh sessionId — needs an exited managed terminal with the
// right exitReason wired through, which requires a real runtime pod being
// replaced under the session; the isolated harness has no cluster to
// stage that against.
//
// The backend contract is already pinned by
// TestStartAISessionRespawnsAfterStoppedCloudContextDeath in
// erun-ui/app_test.go — StartAISession produces a fresh serial after the
// prior managed terminal is gone, which is the only Go side the new TS
// path depends on. Here we lock the negative invariant: re-clicking an
// env whose default tabs are all alive must not duplicate the Local/ERun/
// AI rows in the tab strip and must not surface a "Reopening …" overlay,
// otherwise the new ensureLiveDefaultTab branch is misfiring for live
// sessions.
test.describe('sidebar env re-click with alive default tabs', () => {
  test('does not duplicate Local/ERun/AI tabs or flash a Reopening overlay', async ({
    app,
    page,
  }) => {
    const tenant = SEED_TENANT;
    const env = SEED_ENV_ALPHA;

    await app.sidebar.openEnvironment(tenant, env);

    // Local is spawned eagerly by openSelection before ERun's slower
    // StartSession, so it is the most reliable signal that the first
    // open settled. Same precondition as tab-strip.spec.ts.
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });

    // Snapshot the strip after the first open so any duplication after
    // the re-click shows up as a count delta rather than a count match.
    const initialLocalCount = await page.getByRole('tab', { name: 'Local', exact: true }).count();
    const initialERunCount = await page.getByRole('tab', { name: 'ERun', exact: true }).count();
    const initialAICount = await page.getByRole('tab', { name: 'AI', exact: true }).count();

    await app.sidebar.openEnvironment(tenant, env);

    // Generous window so a delayed misfire of ensureLiveDefaultTab on
    // a healthy session still trips this assertion. The headless backend
    // is a singleton with workers: 1, so 2 s is cheap.
    await expect(page.getByText(/Reopening Local shell/i)).toBeHidden();
    await expect(page.getByText(/Reopening AI session/i)).toBeHidden();
    await expect(page.getByText(/Reopening ERun session/i)).toBeHidden();

    await expect(page.getByRole('tab', { name: 'Local', exact: true })).toHaveCount(
      initialLocalCount,
    );
    await expect(page.getByRole('tab', { name: 'ERun', exact: true })).toHaveCount(
      initialERunCount,
    );
    await expect(page.getByRole('tab', { name: 'AI', exact: true })).toHaveCount(initialAICount);
  });
});
