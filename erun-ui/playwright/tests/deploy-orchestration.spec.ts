import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The desktop deploys an Operator's builds-here agent env by
// composing the pure primitives build -> push -> deploy (root AGENTS.md
// § "Command primitives vs orchestration"), never by typing `erun deploy`
// into the Local shell and never via the `build --deploy` operator shortcut.
// The backend signals an orchestrated deploy by returning
// StartSessionResult.orchestrated=true (StartDeploySession ->
// maybeStartDeployOrchestration); activateLocalAfterCommand then skips
// foreground PTY activation because progress lives in the activity queue.
//
// This spec drives the real Manage-dialog Deploy round-trip against the
// headless backend and locks the integration contract: deploying the seeded
// local-agent env with an empty version returns orchestrated=true. That is the
// reachable, deterministic half — maybeStartDeployOrchestration resolves the
// env type and answers synchronously before its background build subprocess
// matters, and the seeded repo has nothing to build so the subprocess fails
// fast without touching the activity queue.
//
// The per-env-type DECISION across all types (runtime / pinned-version =>
// install by reference; remote-agent => builds in its pod) is covered
// exhaustively by erun-ui/deploy_orchestration_test.go's
// deployNeedsBuildOrchestration table — the real build -> push -> deploy needs
// a Docker daemon and cluster the headless harness deliberately lacks.
test.describe('agent-env deploy orchestration (#558)', () => {
  test('deploying a builds-here agent env returns an orchestrated deploy', async ({
    app,
    page,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    // Empty version field => "deploy the current code"; for the seeded
    // local-agent env that means build -> push -> deploy.
    await expect(app.manageDialog.runtimeVersionInput()).toHaveValue('');

    const deployResponse = page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('StartDeploySession'),
    );
    await app.manageDialog.deploy();
    const payload = (await (await deployResponse).json()) as {
      data?: { orchestrated?: boolean; selection?: { version?: string } };
    };

    // The backend resolved pw/alpha as a builds-here agent env and chose the
    // build -> push -> deploy orchestration over an install-by-reference.
    expect(payload.data?.orchestrated).toBe(true);
    expect(payload.data?.selection?.version ?? '').toBe('');

    // submitManageDeploy closes the dialog once the deploy round-trips; the
    // orchestrated branch leaves no foreground shell to switch to.
    await app.manageDialog.waitForClosed();
  });
});
