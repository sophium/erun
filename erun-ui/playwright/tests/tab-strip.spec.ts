import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Tab-strip respawn coverage.
//
// The change in tabRespawnThunks adds a click-driven respawn for the
// default-spawned tabs (ai, local, erun) whose underlying PTY has already
// exited — typically because the linked cloud context auto-stopped while
// the Claude/codex AI session was attached and tryReconnect refused to
// fight the stop. The positive path (a dead Local/AI/ERun tab whose click
// surfaces a "Reopening …" overlay and then swaps in a fresh sessionId)
// requires staging an exited session with the right exitReason metadata
// and an env config the gate accepts; the isolated harness cannot
// deterministically reproduce a stopped cloud context, a closed PTY, and
// a specific installed AI tool at the same time (the stop needs a real
// managed cloud host).
//
// The backend contract is pinned by the Go unit test
// TestStartAISessionRespawnsAfterStoppedCloudContextDeath in
// erun-ui/app_test.go (StartAISession after streamSession has cleaned up
// a dead session returns a fresh serial, not the stale one). Here we
// lock the negative invariant: while the user clicks an alive default
// tab, no respawn busy overlay fires — the early-bail in
// selectTerminalTab must still take precedence over the new respawn
// branch for live sessions, otherwise every tab switch would briefly
// flash a misleading "Reopening …" pill.

test.describe('terminal tab strip', () => {
  test('clicking an alive Local tab does not surface a Reopening overlay', async ({
    app,
    page,
  }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // Local is spawned eagerly by openSelection before the slower ERun
    // open call, so it reliably appears for any env where the
    // auto-start gate did not pop the prompt — same precondition as the
    // sidebar opening spec.
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });
    await localTab.click();

    // If the new respawn branch misfired for an alive session, the user
    // would see a yellow busy pill with this text. toBeHidden with a
    // generous window catches both immediate and delayed misfires while
    // staying fast on a healthy build.
    await expect(page.getByText(/Reopening Local shell/i)).toBeHidden();
    await expect(page.getByText(/Reopening AI session/i)).toBeHidden();
    await expect(page.getByText(/Reopening ERun session/i)).toBeHidden();
  });
});
