import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Producing a new version is the desktop's explicit "Create & deploy new
// version" action: for a builds-here agent env it composes the pure primitives
// build -> push -> deploy (root AGENTS.md § "Command primitives vs
// orchestration"), never `build --deploy` and never a plain in-shell deploy.
// The backend signals that orchestrated deploy with orchestrated=true, and the
// desktop shows no foreground shell because progress lives in the activity
// queue. The plain Deploy button, by contrast, installs an existing version by
// reference and never builds (#739).
//
// This spec locks only the reachable, deterministic half: the create-version
// action on the seeded local-agent env returns orchestrated=true. The env type
// resolves synchronously before the background build matters, and the seeded
// repo has nothing to build so that build fails fast without ever touching the
// activity queue.
//
// The full per-env-type decision (runtime / pinned-version => install by
// reference; remote-agent => builds in its pod) and the real build -> push ->
// deploy — which needs a Docker daemon and cluster the headless harness
// deliberately lacks — are covered by erun-ui/deploy_orchestration_test.go's
// deployNeedsBuildOrchestration table.
test.describe('agent-env create-version orchestration (#558, #739)', () => {
  test('creating a new version on a builds-here agent env returns an orchestrated deploy', async ({
    app,
    page,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const createResponse = page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('StartCreateVersionSession'),
    );
    await app.manageDialog.createVersionButton().click();
    const payload = (await (await createResponse).json()) as {
      data?: { orchestrated?: boolean };
    };

    // The backend resolved pw/alpha as a builds-here agent env and chose the
    // build -> push -> deploy orchestration.
    expect(payload.data?.orchestrated).toBe(true);

    // The orchestrated branch leaves no foreground shell to switch to, so the
    // dialog just closes once the create-version deploy round-trips.
    await app.manageDialog.waitForClosed();
  });
});
