import { test, expect } from '../fixtures/erunApp.js';

// The Runtime tab separates deploying an existing version (Deploy — installs a
// published version by reference, never builds) from producing a new one
// (Create & deploy new version — build → push → deploy). Only a local-agent env
// owns source to build, so only it shows the create action.
test.describe('manage dialog — deploy vs create new version (#739)', () => {
  test('a local-agent env offers Deploy and Create & deploy new version', async ({
    app,
    seededEnv,
    page,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    // Deploy is visible but disabled until a version is chosen (it installs a
    // version by reference, never an implicit build).
    await expect(app.manageDialog.deployButton()).toBeVisible();
    await expect(app.manageDialog.deployButton()).toBeDisabled();
    const createVersion = app.manageDialog.createVersionButton();
    await expect(createVersion).toBeVisible();
    await expect(createVersion).toContainText('Create & deploy new version');

    // Capture the two-action layout, then the open picker. A local-agent env's
    // checklist is not version-gated (its charts come from the working tree and
    // the create-version flow uses the selection), so the charts show straight
    // away. Freeze animations so the popover is captured fully faded-in.
    await page.screenshot({
      path: 'test-results/runtime-tab-local-agent.png',
      animations: 'disabled',
    });
    await app.manageDialog.openVersionPicker();
    await expect(
      app.manageDialog.deployComponentCheckbox(`${seededEnv.tenant}-devops`),
    ).toBeVisible();
    await expect(app.manageDialog.deployComponentsHint()).toHaveCount(0);
    await page.screenshot({
      path: 'test-results/runtime-tab-version-picker.png',
      animations: 'disabled',
    });

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('a runtime env offers Deploy but not Create & deploy new version', async ({
    app,
    seededRuntimeEnv,
  }) => {
    // A runtime env consumes published versions by reference and owns no source
    // to build, so the create-version action is absent.
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.deployButton()).toBeVisible();
    await expect(app.manageDialog.createVersionButton()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
