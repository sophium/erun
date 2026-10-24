import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

// Only ERun's line publishes charts at these versions, so the chart picker
// offers ERun entries and not the tenant's image line.
async function stubTwoVersionLines(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadVersionSuggestions') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            suggestions: [
              {
                label: 'team last snapshot',
                version: '9.9.9-snapshot-20260101010101',
                source: 'team',
                image: 'registry.example/acme/team-devops',
              },
              {
                label: 'ERun latest stable',
                version: '1.0.178',
                source: 'ERun',
                image: 'ghcr.io/sophium/erun-devops',
              },
            ],
            notices: [],
          },
        }),
      });
    }
    await route.continue();
  });
}

test.describe('manage dialog — runtime chart coordinate (#994)', () => {
  test('offers the ERun chart line, writes a full reference, and persists it', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubTwoVersionLines(page);
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const field = app.manageDialog.runtimeChartInput();
    await expect(field).toBeVisible();

    await expect(field).toHaveValue('');
    await expect(field).toHaveAttribute('placeholder', 'Published with the deployed version');
    await expect(
      app.manageDialog.locator().getByText('Runtime chart', { exact: true }),
    ).toBeVisible();
    await expect(field).toHaveAttribute(
      'aria-describedby',
      'environment-config-runtimechart-helper',
    );

    // Offering the tenant's chartless line would invite the deploy failure this prevents.
    await app.manageDialog.openRuntimeChartPicker();
    await expect(
      page.getByRole('option', { name: /Published with the deployed version/ }),
    ).toBeVisible();
    await expect(page.getByRole('option', { name: /ERun latest stable/ })).toBeVisible();
    await expect(page.getByRole('option', { name: /team last snapshot/ })).toHaveCount(0);

    await app.manageDialog.pickRuntimeChart('ERun latest stable');
    await expect(field).toHaveValue('oci://ghcr.io/sophium/charts/erun-devops:1.0.178');

    expect(await app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(true);
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await expect(app.manageDialog.runtimeChartInput()).toHaveValue(
      'oci://ghcr.io/sophium/charts/erun-devops:1.0.178',
    );

    await app.manageDialog.openRuntimeChartPicker();
    await app.manageDialog.pickRuntimeChart('Published with the deployed version');
    await expect(app.manageDialog.runtimeChartInput()).toHaveValue('');
  });
});

// Chart availability comes from the seeded ERUN_CHART_AVAILABILITY_OVERRIDE:
// 1.0.0 publishes erun-devops, the tenant snapshot publishes nothing.
async function stubChartlessSnapshotAndErunLine(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadVersionSuggestions') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            suggestions: [
              {
                label: 'team last snapshot',
                version: '9.9.9-snapshot-20260101010101',
                source: 'team',
                image: 'registry.example/acme/team-devops',
              },
              {
                label: 'ERun latest stable',
                version: '1.0.0',
                source: 'ERun',
                image: 'ghcr.io/sophium/erun-devops',
              },
            ],
            notices: [],
          },
        }),
      });
    }
    await route.continue();
  });
}

test.describe('manage dialog — a version with no runtime chart (#993)', () => {
  test('is named before Deploy, blocks it, and offers the chart that fixes it', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubChartlessSnapshotAndErunLine(page);
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await app.manageDialog.pickVersion('1.0.0');
    await expect(app.manageDialog.runtimeChartPanelNotice()).toHaveCount(0);

    await app.manageDialog.pickVersion('9.9.9-snapshot-20260101010101');
    await expect(app.manageDialog.runtimeChartPanelNotice()).toContainText(
      'No runtime chart is published at 9.9.9-snapshot-20260101010101',
    );

    await page.keyboard.press('Escape');
    await expect(app.manageDialog.runtimeChartNotice()).toContainText(
      'No runtime chart is published at 9.9.9-snapshot-20260101010101',
    );
    await expect(app.manageDialog.deployButton()).toBeDisabled();
    await expect(app.manageDialog.deployButton()).toHaveAttribute(
      'aria-describedby',
      'environment-config-runtimechart-notice',
    );

    const adopt = app.manageDialog.adoptRuntimeChartButton();
    await expect(adopt).toBeVisible();
    await adopt.click();
    await expect(app.manageDialog.runtimeChartInput()).toHaveValue(
      'oci://ghcr.io/sophium/charts/erun-devops:1.0.0',
    );

    // A deploy installs the saved chart, so Deploy stays disabled until it is saved.
    await expect(app.manageDialog.runtimeChartNotice()).toContainText('unsaved');
    await expect(app.manageDialog.deployButton()).toBeDisabled();
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);

    await expect(app.manageDialog.runtimeChartNotice()).toContainText(
      'Runtime chart erun-devops 1.0.0, set on this environment.',
    );
    await expect(app.manageDialog.deployButton()).toBeEnabled();
  });
});
