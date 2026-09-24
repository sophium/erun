import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

// stubRuntimeRunState makes LoadRuntimeRunState answer with a fixed reading
// instead of falling through to the harness's stub kubectl, which has no
// cluster and so cannot read any Deployment. Mirrors
// manage-runtime-usage.spec.ts's stubRuntimeUsage. A live replica count is
// exactly the state the inert harness cannot produce -- and "already scaled to
// zero" is the state the reported defect lived in, so staging it here is the
// only way the spec can reach it.
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
    // A running runtime, so the control under test is the one that is offered.
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

    // The control lives beside Deploy, which is where the operator is standing
    // when the resource sliders read as capped.
    const stop = app.manageDialog.stopButton();
    await expect(stop).toBeVisible();
    await expect(stop).toBeEnabled();
    await expect(stop).toHaveAccessibleName(/Stop environment/);

    // The runtime's own state is on screen before anything is asked of it, so
    // pressing Stop is predictable rather than a discovery.
    await expect(app.manageDialog.runtimeRunStateLine()).toContainText('Runtime running — 1 of 1');

    // Error prevention (Nielsen #5): the consequence of a side-effecting action
    // is stated before it runs, not discovered after — and it names the way
    // back, so a stopped environment is never a dead end.
    const helper = app.manageDialog.stopHelperText();
    await expect(helper).toContainText('gives their CPU and memory back to the node');
    await expect(helper).toContainText('Work running in the pod stops');
    await expect(helper).toContainText('Click the environment in the sidebar to start it again');
    // The stated scope: this scales the runtime Deployment and nothing else.
    await expect(helper).toContainText(
      'platform components deployed into this environment (added with',
    );

    // The state is part of the control's accessible description, so the reason
    // a disabled Stop is disabled is reachable without activating it.
    await expect(stop).toHaveAccessibleDescription(/Runtime running/);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The reported defect, in its exact state: Stop was pressed on a runtime that
  // was already scaled to zero. The stop path was correct -- a no-op that said
  // so on a terminal line -- but the control never consulted the state `erun
  // stop` itself reads to decide that no-op, so it offered the action anyway and
  // the operator, whose only feedback was a dialog that did not change, read the
  // button as broken.
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

    // The state the operator was missing: said before the click, not inferred
    // from a dialog that did not change afterwards.
    const state = app.manageDialog.runtimeRunStateLine();
    await expect(state).toContainText('Runtime stopped');
    await expect(state).toContainText('Nothing to stop — the runtime already wants no pods.');

    // The control whose only effect would have been a correct no-op is not
    // offered as if it were the action that frees the node's capacity.
    const stop = app.manageDialog.stopButton();
    await expect(stop).toBeDisabled();
    // A disabled control's reason has to be reachable without activating it.
    await expect(stop).toHaveAccessibleDescription(/Nothing to stop/);

    // Disabling is not a dead end: the way back stays on screen.
    await expect(app.manageDialog.stopHelperText()).toContainText(
      'Click the environment in the sidebar to start it again',
    );

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Stop scales the environment's runtime Deployment and nothing else. The
  // platform components rolled out alongside it (`erun deploy --components`)
  // keep running and keep holding their capacity, so they are the pods still
  // standing after a stop -- and the help has to name them before the click,
  // or they get read afterwards as a stop that failed.
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

  // Scaling a real Deployment to zero needs a cluster the inert harness
  // deliberately lacks, so the reachable invariant is the failure path: the
  // stubbed kubectl cannot read the runtime, and the operator must be told so
  // rather than left believing capacity was freed (Nielsen #1, #9). The success
  // path's decisions are owned by erun-common's stop scenarios in
  // erun-integration/stop_test.go and by erun-ui/environment_stop_test.go.
  //
  // The same limitation covers what a stop does to attached tabs: whether a
  // reconnect leaves the environment stopped, and whether the row then renders
  // stopped rather than failed, is decided by a live replica count no stub can
  // produce. Those branches are owned by
  // erun-integration/stop_test.go::real_run_stop_survives_a_session_reconnect,
  // erun-ui/reconnect_loop_test.go::TestRespawnDeclaresItselfAReconnect,
  // erun-ui/environment_stop_test.go::TestRuntimeStoppedForSelectionMapsClusterStateToTheIndicator,
  // and
  // erun-ui/env_ensure_test.go::TestSurfaceEnsureFailureRendersAStoppedRuntimeAsStopped.
  test('a stop that cannot reach the cluster reports the failure instead of claiming success', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    // The in-flight line is replaced by the outcome line, so record both rather
    // than sampling one locator and racing the swap.
    await app.titlebar.recordBanners();
    await app.manageDialog.stopButton().click();

    // The failure reaches the operator; it must not silently go quiet.
    await expect
      .poll(() => app.titlebar.sawBanner('failed to read deployment'), { timeout: 20_000 })
      .toBe(true);
    // The row must not be flagged stopped by a stop that never happened.
    await expect(app.sidebar.envOpenDot(tenant, environment)).toHaveCount(0);

    // The dialog stays open and usable, so the operator can retry or leave.
    await expect(app.manageDialog.stopButton()).toBeEnabled();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
