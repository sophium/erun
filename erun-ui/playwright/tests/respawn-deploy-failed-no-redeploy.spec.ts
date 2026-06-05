import { expect, test } from '../fixtures/erunApp.js';

// Coverage for the deploy-failed respawn guard (feature/449 follow-up).
//
// maybeRespawnDeadDefaultTab now refuses to respawn a dead default tab
// (ai/local/erun) when the env has a failed deploy in the activity queue
// (selectEnvHasFailedDeploy). Reopening such a tab would re-run `erun open`
// and re-deploy the broken env — the same re-deploy storm #447 already stops
// for auto-reconnect (reconnectBlockedByDeployFailure). With the guard, the
// click instead falls through to selectTerminalTab's dead-tab display: the
// captured failure output and the "── deploy failed — not retrying
// automatically … ──" marker, with recovery left to the failed-deploy card
// (Run doctor / Rebuild & redeploy).
//
// The positive path — a dead PTY *and* a `status: failed` deploy activity
// entry for the same env — cannot be staged deterministically in the headless
// harness: it reflects the developer's real ~/.erun/ and has no cluster to
// drive a failing deploy, so neither the exited managed terminal (see the
// bug/368 note in sidebar-reclick-no-duplicate-tabs.spec.ts) nor the failed
// activity entry can be arranged here. The guard itself is a pure selector
// over state.activity.entries (selectEnvHasFailedDeploy in app/selectors.ts).
//
// What this spec locks is the reachable negative invariant: a healthy env
// (no failed deploy — the headless default) must NOT surface the deploy-failed
// marker and must open its default tabs normally, proving the new guard does
// not misfire on the common path and block healthy envs from opening.
test.describe('deploy-failed respawn guard', () => {
  test('healthy env opens normally and shows no deploy-failed marker', async ({ app, page }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);
    const env = envs[0]!;

    await app.sidebar.openEnvironment(tenant, env);

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
    // bug/368 note in sidebar-reclick-no-duplicate-tabs.spec.ts.)
    await expect(page.getByText(/deploy failed — not retrying automatically/i)).toBeHidden();
  });
});
