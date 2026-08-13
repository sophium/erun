import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';

// The runtime chart and the runtime image are two artifacts on two release lines.
// A version names both only while they ride one line, so an env whose image is
// versioned on its project's own line states the chart separately. These stubbed
// suggestions are the two lines the picker groups: the tenant's own image line
// and ERun's. Only ERun's carries charts at those versions -- ERun publishes
// charts/erun-devops and the erun-devops image together -- which is why the chart
// picker offers ERun entries and not the tenant's.
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

    // Visibility of system status (Nielsen #1) and the named default (#3): an env
    // that has stated no chart reads as "published with the deployed version"
    // rather than as an unexplained empty box.
    await expect(field).toHaveValue('');
    await expect(field).toHaveAttribute('placeholder', 'Published with the deployed version');
    // Labelled and described for assistive tech (WCAG 2.4.6, 3.3.2).
    await expect(
      app.manageDialog.locator().getByText('Runtime chart', { exact: true }),
    ).toBeVisible();
    await expect(field).toHaveAttribute(
      'aria-describedby',
      'environment-config-runtimechart-helper',
    );

    // Recognition over recall (#6) and error prevention (#5): the chart versions
    // are offered, and only from the line that actually publishes charts. The
    // tenant's own image line is deliberately absent -- offering it would invite
    // the deploy failure this control exists to prevent.
    await app.manageDialog.openRuntimeChartPicker();
    await expect(
      page.getByRole('option', { name: /Published with the deployed version/ }),
    ).toBeVisible();
    await expect(page.getByRole('option', { name: /ERun latest stable/ })).toBeVisible();
    await expect(page.getByRole('option', { name: /team last snapshot/ })).toHaveCount(0);

    // Picking writes the full reference, so what the env records is unambiguous.
    await app.manageDialog.pickRuntimeChart('ERun latest stable');
    await expect(field).toHaveValue('oci://ghcr.io/sophium/charts/erun-devops:1.0.178');

    // Editing it is an unsaved change on this tab, consistent with every other
    // Runtime field, and saving persists it to the env config.
    expect(await app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(true);
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);
    // Deploy-relevant change: the chart is half of what a deploy installs, so the
    // banner tells the operator it takes effect on the next deploy (Nielsen #1).
    await expect(app.manageDialog.redeployBanner()).toBeVisible();

    // Reopen: the chart persisted to the env config.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');
    await expect(app.manageDialog.runtimeChartInput()).toHaveValue(
      'oci://ghcr.io/sophium/charts/erun-devops:1.0.178',
    );

    // User control and freedom (#3): the named default is the way back, and it
    // clears the field rather than leaving a chart the env no longer rides.
    await app.manageDialog.openRuntimeChartPicker();
    await app.manageDialog.pickRuntimeChart('Published with the deployed version');
    await expect(app.manageDialog.runtimeChartInput()).toHaveValue('');
  });
});

// The version picker names the image's version. When that version has no runtime
// chart -- the case an environment on its project's own release line lands in --
// the deploy cannot succeed, and the operator used to find that out by watching it
// fail. These lock the pre-commitment behaviour: say it, disable the action that
// cannot work, and put the fix one click away.
//
// Chart availability comes from the suite's seeded ERUN_CHART_AVAILABILITY_OVERRIDE
// (fixtures/seedRoot.ts): 1.0.0 publishes erun-devops, and the tenant's own
// snapshot version publishes nothing at all.
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

    // A version on ERun's line publishes a chart, so nothing is blocked and the
    // notice stays quiet -- the control only speaks when it has something to say.
    // The panel stays open after a pick, which is where this is read.
    await app.manageDialog.pickVersion('1.0.0');
    await expect(app.manageDialog.runtimeChartPanelNotice()).toHaveCount(0);

    // The tenant's snapshot version publishes no chart of any kind. The panel says
    // so while the operator is still choosing (Nielsen #1: a first-class blocking
    // line where the decision is made, not a nested detail found later).
    await app.manageDialog.pickVersion('9.9.9-snapshot-20260101010101');
    await expect(app.manageDialog.runtimeChartPanelNotice()).toContainText(
      'No runtime chart is published at 9.9.9-snapshot-20260101010101',
    );

    // Closed, the same statement sits under the version row, beside Deploy.
    await page.keyboard.press('Escape');
    await expect(app.manageDialog.runtimeChartNotice()).toContainText(
      'No runtime chart is published at 9.9.9-snapshot-20260101010101',
    );
    // Error prevention (#5): the action known to fail is not offered, and the
    // button points at the reason for assistive tech.
    await expect(app.manageDialog.deployButton()).toBeDisabled();
    await expect(app.manageDialog.deployButton()).toHaveAttribute(
      'aria-describedby',
      'environment-config-runtimechart-notice',
    );

    // Recognition over recall (#6) and named recovery (#9): the fix is a button
    // labelled with the chart it adopts, and it fills the field below.
    const adopt = app.manageDialog.adoptRuntimeChartButton();
    await expect(adopt).toBeVisible();
    await adopt.click();
    await expect(app.manageDialog.runtimeChartInput()).toHaveValue(
      'oci://ghcr.io/sophium/charts/erun-devops:1.0.0',
    );

    // The chart is chosen but unsaved, and a deploy installs the saved one -- so
    // the notice says so and Deploy stays disabled. Enabling it here would start a
    // rollout that fails for the reason the operator just fixed on screen.
    await expect(app.manageDialog.runtimeChartNotice()).toContainText('unsaved');
    await expect(app.manageDialog.deployButton()).toBeDisabled();
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);

    // Saved: the notice becomes the status line naming what will install, and
    // Deploy is available again.
    await expect(app.manageDialog.runtimeChartNotice()).toContainText(
      'Runtime chart erun-devops 1.0.0, set on this environment.',
    );
    await expect(app.manageDialog.deployButton()).toBeEnabled();
  });
});
