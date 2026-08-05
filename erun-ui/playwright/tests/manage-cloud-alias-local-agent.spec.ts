import { test, expect } from '../fixtures/erunApp.js';
import {
  SEED_CLOUD_ALIAS,
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// Attaching an AWS alias delivers host credentials into the env's runtime pod,
// and the chart wires AWS_PROFILE=erun-host for every env type — but the desktop
// refresher that writes that profile used to skip local-agent envs, leaving those
// pods pointed at a profile nothing ever created.
//
// The delivery itself needs a live pod and an MCP port-forward the inert harness
// deliberately lacks, so what this spec locks is the reachable half: attaching an
// AWS alias to a local-agent env saves and persists, which is the call that now
// reconciles the refresher for this env type (persistEnvironmentConfig ->
// reconcileCloudCredentialsRefresherForSelection). The env-type gate itself is
// covered by TestResolveCloudCredentialsRefreshTargetCoversEveryEnvTypeWithAnAWSAlias
// in erun-ui/cloud_credentials_refresher_test.go.
test.describe('manage dialog AWS alias on a local-agent env', () => {
  test('attaching the alias saves and persists', async ({ app }) => {
    const environment = uniqueEnvironmentName('aws-alias-local');
    // A pinned, deliberately out-of-the-way local port range: once the alias is
    // attached the backend starts a credential refresher that probes this env's
    // MCP port, and a range no real env allocates keeps that probe from reaching
    // an unrelated listener on the developer's machine.
    seedEnvironment(SEED_TENANT, environment, 'localportrangestart: 46500\n');
    try {
      await app.reloadEnvironments();
      await app.sidebar
        .envRowButton(SEED_TENANT, environment)
        .waitFor({ state: 'visible', timeout: 15_000 });

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

      // Reopen: the attachment persisted to the env config, so the refresher the
      // save reconciled has a durable alias behind it.
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
