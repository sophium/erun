import { test, expect } from '../fixtures/erunApp.js';

// Issue #718 — the Runtime tab's "Components to deploy" checklist lets the
// operator see and control exactly what `erun deploy` rolls out (opt-in-only),
// and save that as the env's per-machine default. This spec locks the frontend
// contract on the inert baseline: for a component-only/inert env the checklist
// resolves the runtime item (the published erun-devops chart), pre-checks it,
// gates "Set as default" on a real change, and round-trips a toggle. The
// deploy-set *resolution* (which charts a selection maps to, the published-chart
// fallback) is covered by erun-common integration goldens (deploy_test.go) and
// the config round-trip by erun-ui Go tests; the headless baseline vendors no
// local component charts, so only the runtime item is reachable here.
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

    // The runtime item resolves and is checked by default (bootstrap/heal
    // default), and there is nothing to save yet.
    await expect(runtime).toBeVisible();
    await expect(runtime).toBeChecked();
    await expect(saveDefault).toBeDisabled();

    // The headless baseline vendors no local runtime chart, so the item is the
    // published erun-devops chart. Its label must name that real chart and show
    // <tenant>-devops only as the release name — not present the release name as
    // a published chart (#721). This keeps the checklist consistent with the
    // erun-devops versions the "Version to deploy" picker offers.
    await expect(runtime).toHaveAccessibleName(
      `Runtime — published erun-devops chart (released as ${runtimeName})`,
    );

    // Unchecking is a real change → "Set as default" becomes actionable.
    await runtime.click();
    await expect(runtime).not.toBeChecked();
    await expect(saveDefault).toBeEnabled();

    // Toggling back to the default retires the change (no spurious dirty state).
    await runtime.click();
    await expect(runtime).toBeChecked();
    await expect(saveDefault).toBeDisabled();

    // Saving a changed selection persists it and raises the pending-redeploy
    // banner, because a component-selection change alters what a redeploy rolls
    // out. After the save completes there is nothing left to save.
    await runtime.click();
    await expect(saveDefault).toBeEnabled();
    await saveDefault.click();
    await expect(app.manageDialog.redeployBanner()).toBeVisible();
    await expect(saveDefault).toBeDisabled();
  });
});
