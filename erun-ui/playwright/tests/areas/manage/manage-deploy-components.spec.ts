import type { Page } from '@playwright/test';

import { artifactPath } from '../../../fixtures/artifacts.js';
import { boundingBoxOf } from '../../../fixtures/boundingBox.js';
import { test, expect } from '../../../fixtures/erunApp.js';

// A real ghcr.io lookup returns the build's own version, which the dialog would
// snap to; these versions also key ERUN_CHART_AVAILABILITY_OVERRIDE.
async function stubVersionSuggestions(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadVersionSuggestions') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            suggestions: [
              { label: 'Current', version: '1.0.0' },
              { label: 'Has a subset', version: '1.0.90' },
              { label: 'No charts', version: '1.0.50' },
            ],
            notices: [],
          },
        }),
      });
    }
    await route.continue();
  });
}

// The headless baseline vendors no local component charts, so only the runtime
// item is reachable for a local env.
test.describe('manage dialog — components to deploy (#718)', () => {
  test('runtime is pre-checked, toggling gates Set as default, and it persists', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubVersionSuggestions(page);
    const runtimeName = `${seededEnv.tenant}-devops`;
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.deployButton()).toBeDisabled();

    await app.manageDialog.openVersionPicker();
    await expect(app.manageDialog.deployComponentsHint()).toBeVisible();
    await expect(app.manageDialog.deployComponentCheckbox(runtimeName)).toHaveCount(0);
    await expect(app.manageDialog.saveDeployComponentsButton()).toBeDisabled();

    await app.manageDialog.pickVersion('1.0.0');

    const runtime = app.manageDialog.deployComponentCheckbox(runtimeName);
    const saveDefault = app.manageDialog.saveDeployComponentsButton();

    await expect(app.manageDialog.deployComponentsHeading()).toHaveText(
      'Components in 1.0.0 to deploy',
    );

    await expect(runtime).toBeVisible();
    await expect(runtime).toBeChecked();
    await expect(saveDefault).toBeDisabled();

    // The version, not the env's source, decides which charts exist.
    for (const component of [
      'pw-backend-postgres',
      'pw-backend-db',
      'pw-backend-api',
      'pw-powerdns',
      'pw-docs',
    ]) {
      await expect(app.manageDialog.deployComponentCheckbox(component)).toBeVisible();
    }

    await expect(runtime).toHaveAccessibleName(
      `Runtime — published erun-devops chart (released as ${runtimeName})`,
    );

    await runtime.click();
    await expect(runtime).not.toBeChecked();
    await expect(saveDefault).toBeEnabled();

    await runtime.click();
    await expect(runtime).toBeChecked();
    await expect(saveDefault).toBeDisabled();

    await runtime.click();
    await expect(saveDefault).toBeEnabled();
    await saveDefault.click();
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible();
    await expect(saveDefault).toBeDisabled();

    // The banner's deploy reads the same checklist, so an empty selection refuses there too.
    await expect(app.manageDialog.redeployNowButton()).toBeDisabled();
    await expect(app.manageDialog.redeployBanner()).toContainText('no charts are checked');
  });

  test('the version panel survives focus leaving it while a save settles', async ({
    app,
    page,
    seededEnv,
  }) => {
    // The save disables its own button and focus falls to the dialog container;
    // focus leaving the panel without a pointer must not dismiss it.
    await stubVersionSuggestions(page);
    const runtimeName = `${seededEnv.tenant}-devops`;
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await app.manageDialog.pickVersion('1.0.0');

    const runtime = app.manageDialog.deployComponentCheckbox(runtimeName);
    const saveDefault = app.manageDialog.saveDeployComponentsButton();
    await expect(runtime).toBeChecked();
    await runtime.click();
    await expect(saveDefault).toBeEnabled();
    await saveDefault.click();
    await app.manageDialog.waitForRedeployBanner();
    await expect(saveDefault).toBeDisabled();

    await app.manageDialog.versionPickerSearchInput().focus();
    await app.manageDialog.locator().focus();

    await expect(saveDefault).toBeDisabled();
    await expect(app.manageDialog.deployComponentsHeading()).toBeVisible();
  });

  test('a sourceless (runtime) env offers the publishable platform components by reference', async ({
    app,
    page,
    seededRuntimeEnv,
  }) => {
    await stubVersionSuggestions(page);
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.deployButton()).toBeDisabled();
    await app.manageDialog.openVersionPicker();
    await expect(app.manageDialog.deployComponentsHint()).toBeVisible();
    await expect(app.manageDialog.deployComponentCheckbox('pw-backend-api')).toHaveCount(0);

    await app.manageDialog.pickVersion('1.0.0');

    for (const component of [
      'pw-backend-postgres',
      'pw-backend-db',
      'pw-backend-api',
      'pw-powerdns',
      'pw-docs',
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
    // Chart availability is pinned by ERUN_CHART_AVAILABILITY_OVERRIDE so the probe is offline.
    await stubVersionSuggestions(page);

    const runtimeName = `${seededRuntimeEnv.tenant}-devops`;
    const platform = [
      'pw-backend-postgres',
      'pw-backend-db',
      'pw-backend-api',
      'pw-powerdns',
      'pw-docs',
    ];
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.deployButton()).toBeDisabled();
    await app.manageDialog.openVersionPicker();
    await expect(app.manageDialog.deployComponentsHint()).toBeVisible();
    for (const component of platform) {
      await expect(app.manageDialog.deployComponentCheckbox(component)).toHaveCount(0);
    }
    await page.screenshot({
      path: artifactPath('test-results/runtime-picker-gated.png'),
      animations: 'disabled',
    });

    await app.manageDialog.pickVersion('1.0.0');
    await expect(app.manageDialog.deployButton()).toBeEnabled();
    await expect(app.manageDialog.deployComponentsHeading()).toHaveText(
      'Components in 1.0.0 to deploy',
    );
    for (const component of platform) {
      await expect(app.manageDialog.deployComponentCheckbox(component)).toBeVisible();
    }
    await expect(app.manageDialog.deployComponentCheckbox(runtimeName)).toBeVisible();
    await page.screenshot({
      path: artifactPath('test-results/runtime-picker-populated.png'),
      animations: 'disabled',
    });

    // 1.0.90 published only postgres + db.
    await app.manageDialog.pickVersion('1.0.90');
    await expect(app.manageDialog.deployComponentCheckbox('pw-backend-api')).toHaveCount(0);
    await expect(app.manageDialog.deployComponentCheckbox('pw-powerdns')).toHaveCount(0);
    await expect(app.manageDialog.deployComponentCheckbox('pw-docs')).toHaveCount(0);
    await expect(app.manageDialog.deployComponentCheckbox('pw-backend-postgres')).toBeVisible();
    await expect(app.manageDialog.deployComponentCheckbox('pw-backend-db')).toBeVisible();
    await expect(app.manageDialog.deployComponentCheckbox(runtimeName)).toBeVisible();

    // 1.0.50 published no component charts.
    await app.manageDialog.pickVersion('1.0.50');
    for (const component of platform) {
      await expect(app.manageDialog.deployComponentCheckbox(component)).toHaveCount(0);
    }
    await expect(app.manageDialog.deployComponentCheckbox(runtimeName)).toBeVisible();
  });

  test('a private runtime image surfaces an actionable auth notice in the picker (#749)', async ({
    app,
    seededEnv,
    page,
  }) => {
    // The offline harness can't make ghcr return a real 401.
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              suggestions: [{ label: 'ERun latest stable', version: '1.0.0' }],
              notices: [{ image: 'ghcr.io/sophium/frs-devops', kind: 'auth' }],
            },
          }),
        });
      }
      await route.continue();
    });
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();

    const notices = app.manageDialog.versionSourceNotices();
    await expect(notices).toBeVisible();
    await expect(notices).toContainText('ghcr.io/sophium/frs-devops is private');
    await expect(notices).toContainText('docker login ghcr.io');
  });

  test('the picker stays within the viewport and scrolls to every component', async ({
    app,
    page,
    seededRuntimeEnv,
  }) => {
    const viewportHeight = 720;
    await stubVersionSuggestions(page);
    await page.setViewportSize({ width: 1440, height: viewportHeight });
    await app.sidebar.openManageDialogViaKeyboard(
      seededRuntimeEnv.tenant,
      seededRuntimeEnv.environment,
    );
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.openVersionPicker();
    await app.manageDialog.pickVersion('1.0.0');

    const box = await boundingBoxOf(
      app.manageDialog.versionPickerPopover(),
      'version picker popover',
    );
    expect(box.y + box.height).toBeLessThanOrEqual(viewportHeight + 1);

    const lastComponent = app.manageDialog.deployComponentCheckbox('pw-docs');
    await lastComponent.scrollIntoViewIfNeeded();
    await expect(lastComponent).toBeInViewport();
  });

  async function stubRuntimeDeployRow(page: Page, publishedChart: string): Promise<void> {
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: { suggestions: [{ label: 'Latest stable', version: '1.0.20' }], notices: [] },
          }),
        });
      }
      if (body.method === 'LoadDeployComponents') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              components: [
                {
                  name: 'pw-devops',
                  runtime: true,
                  source: 'published-chart',
                  selected: true,
                  publishedChart,
                },
              ],
              runtimeChart: {
                reference: 'oci://ghcr.io/sophium/charts/erun-devops',
                version: '1.0.20',
                chart: 'erun-devops',
                source: 'canonical',
                missing: false,
                unknown: false,
              },
            },
          }),
        });
      }
      await route.continue();
    });
  }

  test('runtime row names the tenant chart the deploy installs (#767)', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubRuntimeDeployRow(page, 'pw-devops');
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.pickVersion('1.0.20');
    await expect(
      app.manageDialog
        .versionPickerPopover()
        .getByText('Runtime — published pw-devops chart', { exact: true }),
    ).toBeVisible();
  });

  // A resolved runtimeChart leaves the empty checklist as the only thing that can disable Deploy.
  async function stubChecklistOfComponents(page: Page): Promise<void> {
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: { suggestions: [{ label: 'Latest stable', version: '1.0.20' }], notices: [] },
          }),
        });
      }
      if (body.method === 'LoadDeployComponents') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              components: [
                { name: 'pw-devops', runtime: true, source: 'published-chart', selected: true },
                {
                  name: 'pw-backend-api',
                  runtime: false,
                  source: 'published-chart',
                  selected: false,
                },
                { name: 'pw-docs', runtime: false, source: 'published-chart', selected: false },
              ],
              runtimeChart: {
                reference: 'oci://ghcr.io/sophium/charts/erun-devops',
                version: '1.0.20',
                chart: 'erun-devops',
                source: 'canonical',
                missing: false,
                unknown: false,
              },
            },
          }),
        });
      }
      await route.continue();
    });
  }

  // An empty selection passes no --components, which `erun deploy` reads as "runtime only".
  test('an emptied checklist disables Deploy rather than rolling the runtime chart alone (#2384)', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubChecklistOfComponents(page);
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.pickVersion('1.0.20');

    const runtime = app.manageDialog.deployComponentCheckbox('pw-devops');
    await expect(runtime).toBeChecked();
    await expect(app.manageDialog.deployComponentCheckbox('pw-backend-api')).not.toBeChecked();
    await expect(app.manageDialog.deployButton()).toBeEnabled();

    await runtime.click();
    await expect(runtime).not.toBeChecked();
    await expect(app.manageDialog.deployButton()).toBeDisabled();
    const panelNotice = app.manageDialog.deployComponentsEmptyPanelNotice();
    await expect(panelNotice).toContainText('No charts are checked');
    await page.screenshot({
      path: artifactPath('test-results/manage-deploy-components-emptied.png'),
      animations: 'disabled',
    });

    await runtime.click();
    await expect(runtime).toBeChecked();
    await expect(app.manageDialog.deployButton()).toBeEnabled();
    await expect(panelNotice).toHaveCount(0);

    await runtime.click();
    await expect(app.manageDialog.deployButton()).toBeDisabled();
    await page.keyboard.press('Escape');
    await expect(app.manageDialog.deployComponentsEmptyNotice()).toContainText(
      'No charts are checked',
    );
    await expect(app.manageDialog.deployButton()).toBeDisabled();
    await expect(app.manageDialog.deployButton()).toHaveAttribute(
      'aria-describedby',
      'environment-config-deploy-components-empty-notice',
    );
  });

  test('runtime row shows the erun-devops fallback when the tenant chart is unpublished (#767)', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubRuntimeDeployRow(page, 'erun-devops');
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await app.manageDialog.pickVersion('1.0.20');
    await expect(
      app.manageDialog
        .versionPickerPopover()
        .getByText('Runtime — published erun-devops chart (released as pw-devops)', {
          exact: true,
        }),
    ).toBeVisible();
  });
});
