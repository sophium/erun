import { test, expect } from '../fixtures/erunApp.js';

// The "Components to deploy" checklist lets the operator control what `erun deploy`
// rolls out and save it as the env's default. The headless baseline vendors no local
// component charts, so only the runtime item is reachable here; deploy-set resolution
// and the config round-trip are covered by erun-common goldens (deploy_test.go) and
// erun-ui Go tests.
test.describe('manage dialog — components to deploy (#718)', () => {
  test('runtime is pre-checked, toggling gates Set as default, and it persists', async ({
    app,
    seededEnv,
  }) => {
    const runtimeName = `${seededEnv.tenant}-devops`;
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const runtime = app.manageDialog.deployComponentCheckbox(runtimeName);
    const saveDefault = app.manageDialog.saveDeployComponentsButton();

    await expect(runtime).toBeVisible();
    await expect(runtime).toBeChecked();
    await expect(saveDefault).toBeDisabled();

    // The label must name the published erun-devops chart and present <tenant>-devops
    // only as the release name, never as a chart of its own — keeping the checklist
    // consistent with the versions the "Version to deploy" picker offers.
    await expect(runtime).toHaveAccessibleName(
      `Runtime — published erun-devops chart (released as ${runtimeName})`,
    );

    await runtime.click();
    await expect(runtime).not.toBeChecked();
    await expect(saveDefault).toBeEnabled();

    await runtime.click();
    await expect(runtime).toBeChecked();
    await expect(saveDefault).toBeDisabled();

    // Changing the component selection alters what a redeploy rolls out, so saving it
    // must raise the pending-redeploy banner.
    await runtime.click();
    await expect(saveDefault).toBeEnabled();
    await saveDefault.click();
    await expect(app.manageDialog.redeployBanner()).toBeVisible();
    await expect(saveDefault).toBeDisabled();
  });

  test('a sourceless (runtime) env offers the publishable platform components by reference', async ({
    app,
    seededRuntimeEnv,
  }) => {
    // A runtime env has no local charts (RemoteRepo), so the checklist offers
    // each published platform component (deployed by reference) plus the
    // runtime — the operator can select them without any local umbrella.
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    for (const component of [
      'erun-backend-postgres',
      'erun-backend-db',
      'erun-backend-api',
      'erun-powerdns',
      'erun-docs',
    ]) {
      await expect(app.manageDialog.deployComponentCheckbox(component)).toBeVisible();
    }
    await expect(
      app.manageDialog.deployComponentCheckbox(`${seededRuntimeEnv.tenant}-devops`),
    ).toBeVisible();
  });
});
