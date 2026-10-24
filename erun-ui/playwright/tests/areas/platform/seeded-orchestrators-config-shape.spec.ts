import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_ENV_ALPHA,
  SEED_TENANT,
  addOrchestrators,
  removeEnvironment,
  removeOrchestrator,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The desktop rewrites config.yaml with its own marshaller on the first
// orchestrator create; staging on top of that must still yield parseable YAML.
test('staging orchestrators keeps the root config readable after the desktop has rewritten it', async ({
  app,
  page,
}) => {
  const orchestrator = 'config-shape-probe';
  try {
    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();
    await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
    await app.orchestratorDialog.create(orchestrator);
    await app.orchestratorDialog.waitForClosed();

    const environment = uniqueEnvironmentName('config shape');
    const restoreOrchestrators = addOrchestrators(
      ['config-shape-seeded'],
      SEED_TENANT,
      SEED_ENV_ALPHA,
    );
    try {
      seedEnvironment(SEED_TENANT, environment);
      await waitForSeededRow(app, SEED_TENANT, environment);
      await expect(
        page.getByRole('button', { name: /Some configuration could not be read/ }),
      ).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
      // The outer finally removes this spec's own orchestrator, which survives the restore.
      restoreOrchestrators();
    }
  } finally {
    removeOrchestrator(orchestrator);
  }
});
