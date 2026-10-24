import { test, expect } from '../../../fixtures/erunApp.js';
import {
  addERunCloudProviderAlias,
  SEED_ENV_ALPHA,
  SEED_TENANT,
  removeOrchestrator,
} from '../../../fixtures/seedRoot.js';

// The baseline seeds no erun-type alias, so each test stages one and restores
// it. A throwaway orchestrator is used and removed afterwards.
const STAGED_ALIAS = 'erun+api.acme.test@erun';

// Radix's Select.Item rejects '', so "declare none" is carried as this label.
const DECLARE_NONE_LABEL = "This machine's own alias";

test.describe('the orchestrator dialog can declare a platform alias', () => {
  test('offers only the erun aliases, and a chosen one survives create then edit', async ({
    app,
  }) => {
    const name = 'platform-alias-test';
    const restore = addERunCloudProviderAlias(STAGED_ALIAS, 'https://api.acme.test');

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);

      await expect(app.orchestratorDialog.aliasTrigger()).toContainText(DECLARE_NONE_LABEL);

      // Non-erun aliases are not offered: the writer refuses them.
      expect(await app.orchestratorDialog.aliasOptionNames()).toEqual([
        DECLARE_NONE_LABEL,
        STAGED_ALIAS,
      ]);

      await app.orchestratorDialog.setAlias(STAGED_ALIAS);
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      // Reopening reloads from the persisted config.
      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.aliasTrigger('Edit orchestrator')).toContainText(
        STAGED_ALIAS,
      );

      // The save replaces the whole entry, so an alias the dialog failed to carry
      // back would be erased by this unrelated rename.
      const renamed = 'platform-alias-test-renamed';
      await app.orchestratorDialog.nameInput('Edit orchestrator').fill(renamed);
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(renamed);
      await expect(app.orchestratorDialog.nameInput('Edit orchestrator')).toHaveValue(renamed);
      await expect(app.orchestratorDialog.aliasTrigger('Edit orchestrator')).toContainText(
        STAGED_ALIAS,
      );

      await app.orchestratorDialog.setAlias(DECLARE_NONE_LABEL, 'Edit orchestrator');
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(renamed);
      await expect(app.orchestratorDialog.aliasTrigger('Edit orchestrator')).toContainText(
        DECLARE_NONE_LABEL,
      );

      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      restore();
    }
  });

  // Without a matching item the trigger renders blank and the operator cannot
  // see the value the next save will refuse.
  test('keeps a stored alias the machine no longer resolves on the list', async ({ app }) => {
    const name = 'stale-alias-test';
    const restore = addERunCloudProviderAlias(STAGED_ALIAS, 'https://api.acme.test');

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
      await app.orchestratorDialog.setAlias(STAGED_ALIAS);
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      restore();

      await app.sidebar.openOrchestratorDialog(name);
      await expect(app.orchestratorDialog.aliasTrigger('Edit orchestrator')).toContainText(
        STAGED_ALIAS,
      );
      expect(await app.orchestratorDialog.aliasOptionNames('Edit orchestrator')).toEqual([
        DECLARE_NONE_LABEL,
        STAGED_ALIAS,
      ]);

      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
      restore();
    }
  });

  test('offers declaring none when this machine has no erun alias', async ({ app }) => {
    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    await expect(app.orchestratorDialog.aliasTrigger()).toContainText(DECLARE_NONE_LABEL);
    expect(await app.orchestratorDialog.aliasOptionNames()).toEqual([DECLARE_NONE_LABEL]);

    await app.orchestratorDialog.cancel();
  });
});
