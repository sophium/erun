import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// An orchestrator can link either agent type. A remote-agent env is reviewed in a
// workspace-sync mirror the operator may place anywhere; a local-agent env is
// reviewed in its own worktree, which is already on this machine because its pod
// hostPath-mounts it — so that path is derived from the environment and must not
// be offered as an editable field with a folder picker.
//
// Every seeded harness env is local-agent, so this is also the first coverage the
// env-selection list has had at all: before the capability landed the candidate
// list was always empty here. What the harness cannot reach is the wiring behind
// Create (no cluster, no sync) — that is covered by
// TestCreateOrchestratorLinksLocalAgentWithoutSync and
// TestListOrchestratorEnvCandidatesCoversBothAgentTypes in
// erun-ui/orchestrator_test.go. This spec cancels rather than creating, so the
// shared seeded config stays untouched for the other specs.
test.describe('orchestrator links a local-agent environment', () => {
  test('offers the env and shows its worktree as a derived path', async ({ app }) => {
    await app.sidebar.newOrchestratorButton().click();
    await app.orchestratorDialog.waitForOpen();

    // The seeded local-agent env is offered, labelled by which kind of review
    // directory it has (recognition over recall: the operator should not have to
    // remember which env type syncs a mirror).
    const row = app.orchestratorDialog.envRow(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(row).toBeVisible();
    await expect(row).toContainText('worktree on this machine');

    await app.orchestratorDialog.toggleEnv(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(app.orchestratorDialog.envCheckbox(SEED_TENANT, SEED_ENV_ALPHA)).toBeChecked();

    // Derived, so it is shown and not editable: no textbox, no folder picker.
    await expect(app.orchestratorDialog.envDirectoryInput(SEED_TENANT, SEED_ENV_ALPHA)).toHaveCount(
      0,
    );
    await expect(
      app.orchestratorDialog.envChooseDirectoryButton(SEED_TENANT, SEED_ENV_ALPHA),
    ).toHaveCount(0);
    // The path rendered is the env's repository path, which the harness seeds to
    // its own repo dir.
    await expect(app.orchestratorDialog.envBlock(SEED_TENANT, SEED_ENV_ALPHA)).toContainText(
      'repo',
    );

    await app.orchestratorDialog.cancel();
    await app.orchestratorDialog.waitForClosed();
  });
});
