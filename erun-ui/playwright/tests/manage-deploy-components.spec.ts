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

    // A local env deploys its working-tree charts, not version-scoped ones, so
    // the heading stays version-free (unlike a sourceless env — see #737 below).
    await expect(app.manageDialog.deployComponentsHeading()).toHaveText('Components to deploy');

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

  test('offers only the component charts published at the selected deploy version (#737)', async ({
    app,
    page,
    seededRuntimeEnv,
  }) => {
    // Version-aware checklist: charts a version never published must not be
    // offered (deploying one would fail). Chart availability per version is
    // pinned by ERUN_CHART_AVAILABILITY_OVERRIDE (backendEnv) so the probe is
    // offline and deterministic. The runtime item is always kept.
    //
    // Stub the version-suggestion RPC so it returns our test versions: on open
    // the dialog snaps a typed version that isn't among the offered suggestions
    // to the latest one, which would otherwise clobber the version we set (a
    // real ghcr.io lookup returns the build's own version). Including our
    // versions keeps the field on exactly what the test types.
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: [
              { label: 'Current', version: '1.0.0' },
              { label: 'Has a subset', version: '1.0.90' },
              { label: 'No charts', version: '1.0.50' },
            ],
          }),
        });
      }
      await route.continue();
    });

    const runtimeName = `${seededRuntimeEnv.tenant}-devops`;
    const platform = [
      'erun-backend-postgres',
      'erun-backend-db',
      'erun-backend-api',
      'erun-powerdns',
      'erun-docs',
    ];
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    // 1.0.0 published every component chart. A sourceless env version-scopes the
    // heading (contrast the local env above, which stays plain).
    await app.manageDialog.setVersionToDeploy('1.0.0');
    await expect(app.manageDialog.deployComponentsHeading()).toHaveText(
      'Components in 1.0.0 to deploy',
    );
    for (const component of platform) {
      await expect(app.manageDialog.deployComponentCheckbox(component)).toBeVisible();
    }
    await expect(app.manageDialog.deployComponentCheckbox(runtimeName)).toBeVisible();

    // 1.0.90 published only postgres + db; the other three drop off, runtime stays.
    await app.manageDialog.setVersionToDeploy('1.0.90');
    await expect(app.manageDialog.deployComponentCheckbox('erun-backend-api')).toHaveCount(0);
    await expect(app.manageDialog.deployComponentCheckbox('erun-powerdns')).toHaveCount(0);
    await expect(app.manageDialog.deployComponentCheckbox('erun-docs')).toHaveCount(0);
    await expect(app.manageDialog.deployComponentCheckbox('erun-backend-postgres')).toBeVisible();
    await expect(app.manageDialog.deployComponentCheckbox('erun-backend-db')).toBeVisible();
    await expect(app.manageDialog.deployComponentCheckbox(runtimeName)).toBeVisible();

    // 1.0.50 published no component charts; only the runtime item remains.
    await app.manageDialog.setVersionToDeploy('1.0.50');
    for (const component of platform) {
      await expect(app.manageDialog.deployComponentCheckbox(component)).toHaveCount(0);
    }
    await expect(app.manageDialog.deployComponentCheckbox(runtimeName)).toBeVisible();
  });
});
