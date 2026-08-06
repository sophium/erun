import { test, expect } from '../fixtures/erunApp.js';

test.describe('manage dialog runtime activity panel', () => {
  test('the Runtime tab shows what the environment is running, read-only', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    // The panel sits under the resource sliders: once the figures read as
    // capped, "what is this environment holding?" is the next question, and the
    // answer has to be where the question is asked.
    const panel = app.manageDialog.runtimeActivityPanel();
    await expect(panel).toBeVisible();

    // Read-only by default (Nielsen #3, user control): the panel is a reading,
    // and the only control it offers unconditionally is a refresh. Reclaim
    // buttons appear per process group, never as a blanket "clean up".
    const refresh = app.manageDialog.runtimeActivityRefreshButton();
    await expect(refresh).toBeVisible();
    await expect(refresh).toHaveAccessibleName(/Refresh what the runtime is running/);

    // The seeded env is inert and never deployed, so the harness's stub kubectl
    // cannot read a pod. Visibility of system status (Nielsen #1) requires the
    // panel to say so rather than render empty, which would read as "nothing is
    // running" — the exact false-negative this work exists to remove.
    await expect(panel).toContainText(
      /Cannot read what the runtime is running|Open the environment to see what it is running/,
    );

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // A populated panel needs a real runtime pod with real resident processes,
  // which the inert harness deliberately lacks (no cluster). The reachable
  // invariant is the negative: with nothing observable, no reclaim control is
  // offered, so the operator is never given an action that would do nothing.
  // The grouping, the reclaim scoping, and the session-count/running-state
  // agreement are owned by the Go suite —
  // erun-ui/runtime_activity_test.go (TestRuntimeActivityGroupsResourceHoldingProcesses,
  // TestRuntimeActivityGroupsAreReadOnly, TestRuntimeReclaimScriptsAreScopedToBuildLeftovers)
  // and erun-ui/session_heartbeat_test.go (TestRuntimeActivityCountMatchesRunningSessions).
  test('no reclaim control is offered when there is nothing observable to reclaim', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    await expect(app.manageDialog.runtimeActivityPanel()).toBeVisible();
    await expect(app.manageDialog.runtimeReclaimButton('gradle')).toHaveCount(0);
    await expect(app.manageDialog.runtimeReclaimButton('docker-build')).toHaveCount(0);

    // Same rule for the capacity reading's explanation: it exists to say why a
    // real figure is capped, so an unavailable reading must not render one —
    // an explanation without a number is noise. The wording of the explanation
    // itself needs a committed node and is owned by
    // erun-ui/runtime_resources_test.go
    // (TestRuntimeResourceStatusKeepsCurrentRuntimeAllocationAsMinimumCapacity,
    // TestRuntimeResourceStatusSurfacesUnaccountedContainers).
    await expect(app.manageDialog.runtimeCapacityNotice()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
