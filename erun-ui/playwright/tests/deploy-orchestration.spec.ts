import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The desktop deploys an Operator's builds-here agent env by composing the
// pure primitives build -> push -> deploy (root AGENTS.md § "Command
// primitives vs orchestration"), never by typing `erun deploy` into the Local
// shell and never via the `build --deploy` operator shortcut. The backend
// signals that orchestrated deploy with orchestrated=true, and the desktop
// then shows no foreground shell because progress lives in the activity queue.
//
// This spec locks only the reachable, deterministic half: deploying the seeded
// local-agent env with an empty version returns orchestrated=true. The env
// type resolves synchronously before the background build matters, and the
// seeded repo has nothing to build so that build fails fast without ever
// touching the activity queue.
//
// The full per-env-type decision (runtime / pinned-version => install by
// reference; remote-agent => builds in its pod) and the real build -> push ->
// deploy — which needs a Docker daemon and cluster the headless harness
// deliberately lacks — are covered by erun-ui/deploy_orchestration_test.go's
// deployNeedsBuildOrchestration table.
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

    // The orchestrated branch leaves no foreground shell to switch to, so the
    // dialog just closes once the deploy round-trips.
    await app.manageDialog.waitForClosed();
  });
});
