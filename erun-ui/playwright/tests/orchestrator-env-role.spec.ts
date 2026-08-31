import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// erun#1745: OrchestratorEnvConfig.Role could be read (`erun list`) but had no
// writer -- neither the desktop dialog nor the CLI could set it. This locks in
// the dialog's write path: a per-linked-env Role control that appears only for
// a checked environment, defaults to "Not declared" (never silently to "Code"
// or "Build"), and persists across a real CreateOrchestrator/UpdateOrchestrator
// round trip -- not just this render's component state. The CLI's own writer
// (`erun orchestrator set-role`) and the shared legal-values contract both
// surfaces validate against are covered by
// erun-integration/orchestrator_test.go and erun-common's own tests instead;
// this harness has no CLI subprocess to drive.
//
// Creates a throwaway orchestrator rather than reusing the seeded
// SEED_ORCHESTRATOR, the same isolation orchestrator-pacing-nudge.spec.ts
// uses, so this spec's persisted state cannot leak into any other spec
// sharing this worker's backend.
test.describe("the orchestrator dialog can set a linked environment's role", () => {
  test('defaults to Not declared, and a chosen role persists across create then edit', async ({
    app,
  }) => {
    const name = 'env-role-test';

    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();
    await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);

    // Unset is a real state, not silently coerced to either known role.
    await expect(app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA)).toContainText(
      'Not declared',
    );

    await app.orchestratorDialog.setEnvRole(SEED_TENANT, SEED_ENV_ALPHA, 'Build');
    await app.orchestratorDialog.create(name);
    await app.orchestratorDialog.waitForClosed();

    // Reopening reloads from the persisted config: this is what proves the
    // role survived the real CreateOrchestrator round trip, not just that the
    // select control renders whatever it was last set to in memory.
    await app.sidebar.openOrchestratorDialog(name);
    await expect(app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA)).toContainText(
      'Build',
    );

    // Changing it again and saving (UpdateOrchestrator) persists the new
    // value too -- the writer works for an edit, not only for a fresh create.
    await app.orchestratorDialog.setEnvRole(SEED_TENANT, SEED_ENV_ALPHA, 'Code');
    await app.orchestratorDialog.save();
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');

    await app.sidebar.openOrchestratorDialog(name);
    await expect(app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA)).toContainText(
      'Code',
    );

    await app.orchestratorDialog.cancel('Edit orchestrator');
  });

  // erun#1770: a runtime environment used to be listed disabled, unable to
  // link at all. It is now checkable, but carries exactly one legal role, so
  // it gets a plain statement of fact instead of the Code/Build/Not declared
  // Select every other candidate offers, and it persists with no review
  // directory — the operate link's whole point.
  test('a runtime environment offers only the runtime role, with no review directory, and persists that way', async ({
    app,
    seededRuntimeEnv,
  }) => {
    const name = 'runtime-role-test';

    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    // Checkable, not greyed out -- the fix's whole point.
    await expect(
      app.orchestratorDialog.envCheckbox(SEED_TENANT, seededRuntimeEnv.environment),
    ).toBeEnabled();
    await app.orchestratorDialog.toggleEnv(SEED_TENANT, seededRuntimeEnv.environment);

    // No mirror/worktree directory controls -- there is nothing to review.
    await expect(
      app.orchestratorDialog.envDirectoryInput(SEED_TENANT, seededRuntimeEnv.environment),
    ).toHaveCount(0);

    // No role Select either -- one legal value is stated, not offered as a choice.
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

    // Reopening reloads from the persisted config: the runtime role and its
    // absent review directory both survived the real CreateOrchestrator
    // round trip, not just this render's component state.
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
