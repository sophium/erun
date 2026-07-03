import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The positive respawn path — clicking a dead default tab (Local/AI/ERun)
// whose PTY exited after its cloud context auto-stopped, surfacing a
// "Reopening …" overlay before a fresh session swaps in — needs a stopped
// managed cloud host the isolated harness cannot reproduce; the Go unit
// test TestStartAISessionRespawnsAfterStoppedCloudContextDeath in
// erun-ui/app_test.go owns that branch. Here we lock the negative
// invariant the harness can reach: clicking an alive default tab must not
// flash a "Reopening …" pill, so the live-session early-bail keeps taking
// precedence over the respawn branch.

test.describe('terminal tab strip', () => {
  test('clicking an alive Local tab does not surface a Reopening overlay', async ({
    app,
    page,
  }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // Local spawns eagerly ahead of the slower ERun open, so it reliably
    // appears for any env where the auto-start gate did not pop a prompt.
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });
    await localTab.click();

    await expect(page.getByText(/Reopening Local shell/i)).toBeHidden();
    await expect(page.getByText(/Reopening AI session/i)).toBeHidden();
    await expect(page.getByText(/Reopening ERun session/i)).toBeHidden();
  });
});
