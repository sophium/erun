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

// The suite hand-writes the root config.yaml, but it is not the only writer:
// the desktop re-emits the whole file through its own YAML marshaller the
// first time any spec creates an orchestrator, at a different sequence
// indentation and free to place further top-level keys below `orchestrators:`.
// addOrchestrators used to append its entries at end-of-file at a fixed
// indentation, which against that file is unparseable YAML.
//
// The cost was paid by whichever spec ran next, not by this one. A root config
// that does not parse is stable, not a torn write a reader can wait out, so
// every later read reports it corrupt, LoadState degrades to zero tenants
// behind a "could not be read" banner, and any spec sharing that worker which
// waits for a row it seeded runs out its own timeout with no clue why. Because
// which specs share a worker changes run to run, so did the spec that failed.
//
// This locks the two writers together in one spec: let the desktop write the
// file, then stage on top of it, then prove the config is still readable by
// the observable every seeding spec depends on.
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
      // waitForSeededRow is the exact step that hung: it re-drives a reload until
      // the row appears, and an unreadable root config means it never can.
      seedEnvironment(SEED_TENANT, environment);
      await waitForSeededRow(app, SEED_TENANT, environment);
      await expect(
        page.getByRole('button', { name: /Some configuration could not be read/ }),
      ).toHaveCount(0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
      // restoreOrchestrators() reverts to the config it read right after this
      // spec's own real orchestrator create above, so that entry survives
      // the restore -- the outer finally removes it explicitly.
      restoreOrchestrators();
    }
  } finally {
    removeOrchestrator(orchestrator);
  }
});
