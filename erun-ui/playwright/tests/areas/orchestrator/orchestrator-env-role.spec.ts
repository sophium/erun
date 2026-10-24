import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT, removeOrchestrator } from '../../../fixtures/seedRoot.js';

// Uses a throwaway orchestrator, removed explicitly, because the round trip
// writes into the shared root config.yaml.
test.describe("the orchestrator dialog can set a linked environment's role", () => {
  test('defaults to Not declared, and a chosen role persists across create then edit', async ({
    app,
  }) => {
    const name = 'env-role-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);

      await expect(
        app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA),
      ).toContainText('Not declared');

      await app.orchestratorDialog.setEnvRole(SEED_TENANT, SEED_ENV_ALPHA, 'Build');
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      // Reopening reloads from the persisted config.
      await app.sidebar.openOrchestratorDialog(name);
      await expect(
        app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA),
      ).toContainText('Build');

      await app.orchestratorDialog.setEnvRole(SEED_TENANT, SEED_ENV_ALPHA, 'Code');
      await app.orchestratorDialog.save();
      await app.orchestratorDialog.waitForClosed('Edit orchestrator');

      await app.sidebar.openOrchestratorDialog(name);
      await expect(
        app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA),
      ).toContainText('Code');

      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
    }
  });

  test('a runtime environment offers only the runtime role, with no review directory, and persists that way', async ({
    app,
    seededRuntimeEnv,
  }) => {
    const name = 'runtime-role-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();

      await expect(
        app.orchestratorDialog.envCheckbox(SEED_TENANT, seededRuntimeEnv.environment),
      ).toBeEnabled();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, seededRuntimeEnv.environment);

      await expect(
        app.orchestratorDialog.envDirectoryInput(SEED_TENANT, seededRuntimeEnv.environment),
      ).toHaveCount(0);

      await expect(
        app.orchestratorDialog.envRoleTrigger(SEED_TENANT, seededRuntimeEnv.environment),
      ).toHaveCount(0);
      const requiredRoleText = app.orchestratorDialog.envRequiredRoleText(
        SEED_TENANT,
        seededRuntimeEnv.environment,
      );
      await expect(requiredRoleText).toBeVisible();
      await expect(requiredRoleText).toContainText('no worktree to review');
      await expect(requiredRoleText).toContainText('no in-pod agent to delegate to');

      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await app.sidebar.openOrchestratorDialog(name);
      await expect(
        app.orchestratorDialog.envCheckbox(
          SEED_TENANT,
          seededRuntimeEnv.environment,
          'Edit orchestrator',
        ),
      ).toBeChecked();
      await expect(
        app.orchestratorDialog.envRequiredRoleText(
          SEED_TENANT,
          seededRuntimeEnv.environment,
          'Edit orchestrator',
        ),
      ).toBeVisible();

      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
    }
  });

  // A host env must not offer the runtime role: deploy, pin and observe all
  // refuse a host env.
  test('a host environment is linkable, reviewed in place, and offered no runtime role', async ({
    app,
    seededHostEnv,
  }) => {
    const name = 'host-role-test';
    const { tenant, environment } = seededHostEnv;

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();

      await expect(app.orchestratorDialog.envCheckbox(tenant, environment)).toBeEnabled();
      await app.orchestratorDialog.toggleEnv(tenant, environment);

      // "Worktree" here means a pod's hostPath mount; a host env has no pod.
      await expect(app.orchestratorDialog.envBlock(tenant, environment)).toContainText(
        'directory on this machine',
      );

      await expect(app.orchestratorDialog.envDirectoryInput(tenant, environment)).toHaveCount(0);

      await expect(app.orchestratorDialog.envRoleTrigger(tenant, environment)).toBeVisible();
      expect(
        await app.orchestratorDialog.envRoleOptionNames(tenant, environment),
        'Runtime must not be offerable for a host environment',
      ).toEqual(['Not declared', 'Code', 'Build']);

      await app.orchestratorDialog.setEnvRole(tenant, environment, 'Build');
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      await app.sidebar.openOrchestratorDialog(name);
      await expect(
        app.orchestratorDialog.envCheckbox(tenant, environment, 'Edit orchestrator'),
      ).toBeChecked();
      await expect(app.orchestratorDialog.envRoleTrigger(tenant, environment)).toContainText(
        'Build',
      );
      expect(
        await app.orchestratorDialog.envRoleOptionNames(tenant, environment),
        'the reopened link must stay free of the runtime role',
      ).toEqual(['Not declared', 'Code', 'Build']);

      await app.orchestratorDialog.cancel('Edit orchestrator');
    } finally {
      removeOrchestrator(name);
    }
  });

  test('the role control appears only for a checked environment', async ({ app }) => {
    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    await expect(app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA)).toHaveCount(0);

    await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA)).toBeVisible();

    await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA)).toHaveCount(0);

    await app.orchestratorDialog.cancel();
  });
});
