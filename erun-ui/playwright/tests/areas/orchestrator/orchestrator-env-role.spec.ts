import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT, removeOrchestrator } from '../../../fixtures/seedRoot.js';

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
// sharing this worker's backend. The Create/UpdateOrchestrator round trip
// this spec exists to prove writes straight into the shared root
// config.yaml -- there is no dialog affordance to delete it again, so the
// created entry must be removed explicitly (removeOrchestrator) rather than
// left for every later spec in this worker to see indefinitely.
test.describe("the orchestrator dialog can set a linked environment's role", () => {
  test('defaults to Not declared, and a chosen role persists across create then edit', async ({
    app,
  }) => {
    const name = 'env-role-test';

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();
      await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);

      // Unset is a real state, not silently coerced to either known role.
      await expect(
        app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA),
      ).toContainText('Not declared');

      await app.orchestratorDialog.setEnvRole(SEED_TENANT, SEED_ENV_ALPHA, 'Build');
      await app.orchestratorDialog.create(name);
      await app.orchestratorDialog.waitForClosed();

      // Reopening reloads from the persisted config: this is what proves the
      // role survived the real CreateOrchestrator round trip, not just that the
      // select control renders whatever it was last set to in memory.
      await app.sidebar.openOrchestratorDialog(name);
      await expect(
        app.orchestratorDialog.envRoleTrigger(SEED_TENANT, SEED_ENV_ALPHA),
      ).toContainText('Build');

      // Changing it again and saving (UpdateOrchestrator) persists the new
      // value too -- the writer works for an edit, not only for a fresh create.
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

    try {
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
    } finally {
      removeOrchestrator(name);
    }
  });

  // A host environment — a directory on the operator's own machine
  // with no pod and no cluster at all — had no way into this picker. It is now
  // checkable like any other env, and it is reviewed in place rather than in a
  // pod's worktree. What it must NOT offer is the runtime role: that role means
  // "operated directly — deploy, pin, observe", and every one of those verbs
  // refuses a host env outright. Offering it would promise a relationship the
  // link cannot deliver, and the mismatch would surface only later, in the
  // orchestrator session, as an environment tool that never works. The shared
  // gate both this picker and the CLI validate against is covered by
  // TestSetOrchestratorEnvRoleRefusesRuntimeForAHostEnvironment in
  // erun-common/orchestrator_env_role_test.go; this spec drives the rendering
  // and the persistence that Go test cannot reach.
  test('a host environment is linkable, reviewed in place, and offered no runtime role', async ({
    app,
    seededHostEnv,
  }) => {
    const name = 'host-role-test';
    const { tenant, environment } = seededHostEnv;

    try {
      await app.sidebar.newOrchestratorButton().click();
      await app.orchestratorDialog.waitForOpen();

      // Checkable, not greyed out with a reason — the fix's whole point.
      await expect(app.orchestratorDialog.envCheckbox(tenant, environment)).toBeEnabled();
      await app.orchestratorDialog.toggleEnv(tenant, environment);

      // Named a directory, not a worktree: in this dialog a worktree is a
      // pod's, hostPath-mounted into it, and a host env has no pod at all, so
      // the same word would describe two different relationships.
      await expect(app.orchestratorDialog.envBlock(tenant, environment)).toContainText(
        'directory on this machine',
      );

      // No mirror to place: a host env's directory is derived from its own
      // repository path and shown read-only rather than offered for editing.
      await expect(app.orchestratorDialog.envDirectoryInput(tenant, environment)).toHaveCount(0);

      // A host env carries more than one legal role, so it gets the real Select
      // — unlike a runtime env, which states its one role as a plain fact.
      await expect(app.orchestratorDialog.envRoleTrigger(tenant, environment)).toBeVisible();
      expect(
        await app.orchestratorDialog.envRoleOptionNames(tenant, environment),
        'Runtime must not be offerable for a host environment',
      ).toEqual(['Not declared', 'Code', 'Build']);

      // It persists as a host link carrying the chosen role, across a real
      // CreateOrchestrator round trip rather than only this render's state.
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
