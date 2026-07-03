import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Coverage for the deploy-failed respawn guard.
//
// maybeRespawnDeadDefaultTab now refuses to respawn a dead default tab
// (ai/local/erun) when the env has a failed deploy in the activity queue
// (selectEnvHasFailedDeploy). Reopening such a tab would re-run `erun open`
// and re-deploy the broken env — the same re-deploy storm already stops
// for auto-reconnect (reconnectBlockedByDeployFailure). With the guard, the
// click instead falls through to selectTerminalTab's dead-tab display: the
// captured failure output and the "── deploy failed — not retrying
// automatically … ──" marker, with recovery left to the failed-deploy card
// (Run doctor / Rebuild & redeploy).
//
// The positive path — a dead PTY *and* a `status: failed` deploy activity
// entry for the same env — cannot be staged deterministically in the headless
// harness: it has no cluster to drive a real failing deploy, so the exited
// managed terminal (see the note in
// sidebar-reclick-no-duplicate-tabs.spec.ts) and the failed activity entry
// cannot be arranged together here. The guard itself is a pure selector
// over state.activity.entries (selectEnvHasFailedDeploy in app/selectors.ts).
//
// What this spec locks is the reachable negative invariant: a healthy env
// (no failed deploy — the headless default) must NOT surface the deploy-failed
// marker and must open its default tabs normally, proving the new guard does
// not misfire on the common path and block healthy envs from opening.
test.describe('deploy-failed respawn guard', () => {
  test('healthy env opens normally and shows no deploy-failed marker', async ({ app, page }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // Local is spawned eagerly by openSelection, so it is the most reliable
    // signal that the open settled (same precondition as the sibling specs).
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });

    // The guard is inactive for a healthy env: the open settles (Local is
    // present) and the deploy-failed marker never appears. If
    // selectEnvHasFailedDeploy misfired on a healthy env, the open/respawn
    // flow would surface the "── deploy failed — not retrying automatically …"
    // marker. (AI/ERun tab presence is not asserted: those depend on a
    // reachable runtime the headless harness has no cluster for — see the
    // note in sidebar-reclick-no-duplicate-tabs.spec.ts.)
    await expect(page.getByText(/deploy failed — not retrying automatically/i)).toBeHidden();
  });
});
