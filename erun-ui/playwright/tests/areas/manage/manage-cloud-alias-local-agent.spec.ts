import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_CLOUD_ALIAS,
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// Credential delivery needs a live pod, so this locks the reachable half: the
// alias saves and persists on a local-agent env, which reconciles its refresher.
test.describe('manage dialog AWS alias on a local-agent env', () => {
  test('attaching the alias saves and persists', async ({ app }) => {
    const environment = uniqueEnvironmentName('aws-alias-local');
    // A range no real env allocates keeps the refresher's MCP probe off unrelated listeners.
    seedEnvironment(SEED_TENANT, environment, 'localportrangestart: 46500\n');
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);

      await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, environment);
      await app.manageDialog.waitForOpen();
      expect(await app.manageDialog.cloudAliasSelectVisible()).toBe(true);
      await expect
        .poll(() => app.manageDialog.cloudAliasSelectedValue())
        .toBe('Select cloud alias');

      await app.manageDialog.chooseCloudAlias(SEED_CLOUD_ALIAS);
      await expect.poll(() => app.manageDialog.cloudAliasSelectedValue()).toBe(SEED_CLOUD_ALIAS);
      await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(true);

      await app.manageDialog.save();
      await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

      await app.manageDialog.cancel();
      await app.manageDialog.waitForClosed();
      await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, environment);
      await app.manageDialog.waitForOpen();
      await expect.poll(() => app.manageDialog.cloudAliasSelectedValue()).toBe(SEED_CLOUD_ALIAS);

      await app.manageDialog.cancel();
      await app.manageDialog.waitForClosed();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});
