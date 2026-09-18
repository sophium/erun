import { test, expect } from '../../../fixtures/erunApp.js';

test.describe('manage dialog runtime stop control', () => {
  test('the Runtime tab offers Stop with its consequence and its recovery named', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    // The control lives beside Deploy, which is where the operator is standing
    // when the resource sliders read as capped.
    const stop = app.manageDialog.stopButton();
    await expect(stop).toBeVisible();
    await expect(stop).toBeEnabled();
    await expect(stop).toHaveAccessibleName(/Stop environment/);

    // Error prevention (Nielsen #5): the consequence of a side-effecting action
    // is stated before it runs, not discovered after — and it names the way
    // back, so a stopped environment is never a dead end.
    const helper = app.manageDialog.stopHelperText();
    await expect(helper).toContainText('gives its CPU and memory back to the node');
    await expect(helper).toContainText('Work running in the pod stops');
    await expect(helper).toContainText('Click the environment in the sidebar to start it again');
    await expect(stop).toHaveAttribute('aria-describedby', 'environment-config-stop-help');

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
