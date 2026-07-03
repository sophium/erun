import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The positive respawn path — a dead tab respawning to a fresh session on
// sidebar re-click — needs a real runtime pod the isolated harness has no
// cluster to stage; that branch is pinned by
// TestStartAISessionRespawnsAfterStoppedCloudContextDeath in
// erun-ui/app_test.go. This spec locks only the reachable negative
// invariant: re-clicking an env whose default tabs are all alive must not
// duplicate tabs or flash a "Reopening" overlay.
test.describe('sidebar env re-click with alive default tabs', () => {
  test('does not duplicate Local/ERun/AI tabs or flash a Reopening overlay', async ({
    app,
    page,
  }) => {
    const tenant = SEED_TENANT;
    const env = SEED_ENV_ALPHA;

    await app.sidebar.openEnvironment(tenant, env);

    // Local spawns eagerly, before ERun's slower StartSession, so it is the
    // most reliable signal that the first open has settled.
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });

    const initialLocalCount = await page.getByRole('tab', { name: 'Local', exact: true }).count();
    const initialERunCount = await page.getByRole('tab', { name: 'ERun', exact: true }).count();
    const initialAICount = await page.getByRole('tab', { name: 'AI', exact: true }).count();

    await app.sidebar.openEnvironment(tenant, env);

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
