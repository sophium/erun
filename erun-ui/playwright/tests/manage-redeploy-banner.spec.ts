import { test, expect } from '../fixtures/erunApp.js';
import type { AppShell } from '../pages/index.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Issue #460 — two mirror-image defects on the Manage dialog's Runtime tab:
// the "Pending redeploy" banner fired on every save, including saves that
// only touched fields the running pod never sees (autoUpgrade/upgradeChannel
// select a future `erun upgrade` run; autoStart is desktop open-time
// behaviour), prompting a pointless pod roll — while editing those same
// fields never lit the per-tab unsaved-changes dot. Both now derive from one
// classification: the dot reflects every editable field on the tab, the
// banner only pod-shaping ones (deployRelevantSignature).
//
// Saves are stubbed over the /__erun_invoke bridge (the
// sidebar-upgrade-all.spec.ts technique), echoing the submitted config back
// as the save result — the seeded config is never written, while the
// dialog behaves exactly as after a real save (initialConfig refresh,
// pendingRedeploy computation).
test.describe('manage dialog redeploy banner scoping (#460)', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as {
        method: string;
        args: unknown[];
      };
      if (body.method === 'SaveEnvironmentConfig') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: body.args[1] }),
        });
      }
      await route.continue();
    });
  });

  test('editing autoUpgrade lights the Runtime dot and clears when reverted', async ({ app }) => {
    await openFirstEnvManageDialog(app);
    await app.manageDialog.selectTab('Runtime');

    expect(await app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);
    await app.manageDialog.autoUpgradeCheckbox().click();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(true);
    await app.manageDialog.autoUpgradeCheckbox().click();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('the banner skips metadata-only saves, fires for pod-shaping ones, and sticks', async ({
    app,
  }) => {
    await openFirstEnvManageDialog(app);
    await app.manageDialog.selectTab('Runtime');

    // Save with only autoUpgrade toggled → nothing the pod needs changed →
    // no banner.
    await app.manageDialog.autoUpgradeCheckbox().click();
    await app.manageDialog.save();
    // The dot clearing proves the save round-tripped (initialConfig refresh).
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);
    await expect(app.manageDialog.redeployBanner()).toHaveCount(0);

    // Save with a pod-shaping change (idle timeout → helm idle.* pod env) →
    // banner appears.
    const idle = app.manageDialog.idleTimeoutInput();
    const original = await idle.inputValue();
    await idle.fill(original === '7m' ? '9m' : '7m');
    await app.manageDialog.save();
    await expect(app.manageDialog.redeployBanner()).toBeVisible();

    // A later metadata-only save must not clear a redeploy the user still
    // owes the pod: toggle autoUpgrade back and save again — banner stays.
    await app.manageDialog.autoUpgradeCheckbox().click();
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime')).toBe(false);
    await expect(app.manageDialog.redeployBanner()).toBeVisible();

    // Close without clicking "Redeploy now"; the stubbed saves never touched
    // the real config, so there is nothing to restore.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

// openFirstEnvManageDialog opens the Manage dialog for the seeded baseline
// env via the keyboard path (mouse row clicks can be intercepted by the env
// hover-card popover on crowded sidebars).
async function openFirstEnvManageDialog(app: AppShell): Promise<void> {
  await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
  await app.manageDialog.waitForOpen();
}
