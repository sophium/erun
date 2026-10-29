import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Only the reachable half: create-version on a local-agent env reports orchestrated=true.
// The per-type decision is covered by deploy_orchestration_test.go.
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

    expect(payload.data?.orchestrated).toBe(true);

    // No foreground shell to switch to.
    await app.manageDialog.waitForClosed();
  });
});
