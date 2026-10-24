import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT, removeOrchestrator } from '../../../fixtures/seedRoot.js';

// Pacing nudges and crash respawn need a real PTY the inert claude stub never
// provides, so only the negative invariant is reachable here.
test.describe('orchestrator pacing/respawn has no surface without a live session', () => {
  test('a created-but-never-started orchestrator stays stopped with no busy/shell/alert artifacts', async ({
    app,
  }) => {
    const name = 'pacing-nudge-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await expect(app.sidebar.orchestratorStatusDot(name, 'stopped')).toBeVisible();
      await expect(app.sidebar.orchestratorBusySpinner(name)).toHaveCount(0);
      await expect(app.sidebar.orchestratorShellSpinner(name)).toHaveCount(0);
      await expect(app.sidebar.orchestratorsAlert()).toHaveCount(0);
    } finally {
      removeOrchestrator(name);
    }
  });
});
