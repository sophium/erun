import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

// A live replica count can't be produced by the harness, so the run state is stubbed.
async function stubRuntimeRunState(page: Page, body: unknown): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (parsed.method === 'LoadRuntimeRunState') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: body }),
      });
    }
    await route.continue();
  });
}

test.describe('manage dialog runtime stop control', () => {
  test('the Runtime tab offers Stop with its consequence and its recovery named', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeRunState(app.page, {
      tenant,
      environment,
      present: true,
      desiredReplicas: 1,
      readyReplicas: 1,
      stopped: false,
    });
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const stop = app.manageDialog.stopButton();
    await expect(stop).toBeVisible();
    await expect(stop).toBeEnabled();
    await expect(stop).toHaveAccessibleName(/Stop environment/);

    await expect(app.manageDialog.runtimeRunStateLine()).toContainText('Runtime running — 1 of 1');

    const helper = app.manageDialog.stopHelperText();
    await expect(helper).toContainText('gives their CPU and memory back to the node');
    await expect(helper).toContainText('Work running in the pod stops');
    await expect(helper).toContainText('Click the environment in the sidebar to start it again');
    await expect(helper).toContainText(
      'platform components deployed into this environment (added with',
    );

    await expect(stop).toHaveAccessibleDescription(/Runtime running/);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Stop on a runtime already scaled to zero must not be offered.
  test('an already-stopped runtime says so where the button is, and Stop is not offered', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeRunState(app.page, {
      tenant,
      environment,
      present: true,
      desiredReplicas: 0,
      readyReplicas: 0,
      stopped: true,
    });
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const state = app.manageDialog.runtimeRunStateLine();
    await expect(state).toContainText('Runtime stopped');
    await expect(state).toContainText('Nothing to stop — the runtime already wants no pods.');

    const stop = app.manageDialog.stopButton();
    await expect(stop).toBeDisabled();
    await expect(stop).toHaveAccessibleDescription(/Nothing to stop/);

    await expect(app.manageDialog.stopHelperText()).toContainText(
      'Click the environment in the sidebar to start it again',
    );

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Components rolled out alongside keep running, so the help names them before the click.
  test('the help names the platform components this stop will leave running', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await stubRuntimeRunState(app.page, {
      tenant,
      environment,
      present: true,
      desiredReplicas: 1,
      readyReplicas: 1,
      stopped: false,
      remainingComponents: [`${tenant}-backend-api`, `${tenant}-backend-postgres`],
    });
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.stopHelperText()).toContainText(
      `keep running and keep holding their capacity: ${tenant}-backend-api, ${tenant}-backend-postgres.`,
    );
    await expect(app.manageDialog.stopButton()).toBeEnabled();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Scaling a real Deployment needs a cluster, so only the failure path is
  // reachable; success and reconnect behaviour are covered in Go.
  test('a stop that cannot reach the cluster reports the failure instead of claiming success', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    // The in-flight line is replaced by the outcome line, so record both.
    await app.titlebar.recordBanners();
    await app.manageDialog.stopButton().click();

    await expect
      .poll(() => app.titlebar.sawBanner('failed to read deployment'), { timeout: 20_000 })
      .toBe(true);
    // The row must not be flagged stopped by a stop that never happened.
    await expect(app.sidebar.envOpenDot(tenant, environment)).toHaveCount(0);

    await expect(app.manageDialog.stopButton()).toBeEnabled();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
