import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The deploy-failed respawn guard stops a dead default tab from being
// reopened when the env has a failed deploy — reopening would re-run
// `erun open` and re-deploy the already-broken env.
//
// Only the negative path is reachable here: the headless harness has no
// cluster to drive a real failing deploy, so a dead PTY plus a failed-deploy
// entry for the same env cannot be staged together. The guard itself is a
// pure activity-queue selector (selectEnvHasFailedDeploy in app/selectors.ts).
//
// This spec locks the reachable invariant: a healthy env opens its tabs
// normally and never surfaces the deploy-failed marker, proving the guard
// does not misfire on the common path.
test.describe('deploy-failed respawn guard', () => {
  test('healthy env opens normally and shows no deploy-failed marker', async ({ app, page }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // Local is spawned eagerly on open, so its tab is the most reliable
    // signal that the open settled.
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });

    // AI/ERun tabs are not asserted — they need a runtime the headless
    // harness has no cluster for.
    await expect(page.getByText(/deploy failed — not retrying automatically/i)).toBeHidden();
  });
});
