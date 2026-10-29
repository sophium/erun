import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// A local-agent env's review directory is derived from the env, so it must not
// be editable. Cancels rather than creates to keep the seeded config untouched.
test.describe('orchestrator links a local-agent environment', () => {
  test('offers the env and shows its worktree as a derived path', async ({ app }) => {
    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    const row = app.orchestratorDialog.envRow(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(row).toBeVisible();
    await expect(row).toContainText('worktree on this machine');

    await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(app.orchestratorDialog.envCheckbox(SEED_TENANT, SEED_ENV_ALPHA)).toBeChecked();

    await expect(app.orchestratorDialog.envDirectoryInput(SEED_TENANT, SEED_ENV_ALPHA)).toHaveCount(
      0,
    );
    await expect(
      app.orchestratorDialog.envChooseDirectoryButton(SEED_TENANT, SEED_ENV_ALPHA),
    ).toHaveCount(0);
    await expect(app.orchestratorDialog.envBlock(SEED_TENANT, SEED_ENV_ALPHA)).toContainText(
      'repo',
    );

    await app.orchestratorDialog.cancel();
    await app.orchestratorDialog.waitForClosed();
  });
});
