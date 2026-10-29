import { test, expect } from '../../../fixtures/erunApp.js';

test.describe('manage dialog runtime activity panel', () => {
  test('the Runtime tab shows what the environment is running, read-only', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openManageDialogViaKeyboard(tenant, environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Runtime');

    const panel = app.manageDialog.runtimeActivityPanel();
    await expect(panel).toBeVisible();

    // Read-only: reclaim buttons appear per process group, never as a blanket clean-up.
    const refresh = app.manageDialog.runtimeActivityRefreshButton();
    await expect(refresh).toBeVisible();
    await expect(refresh).toHaveAccessibleName(/Refresh what the runtime is running/);

    // The inert env can't be read; an empty panel would falsely read as "nothing running".
    // Timeout vs. signal-kill classification is covered in Go.
    await expect(panel).toContainText(
      /Cannot read what the runtime is running|Open the environment to see what it is running/,
    );
    await expect(panel).not.toContainText('signal:');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Without a real pod, only the negative is reachable: no reclaim control when nothing is observable.
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

    // An explanation without a number is noise.
    await expect(app.manageDialog.runtimeCapacityNotice()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
