import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The dot reflects every editable field; the banner only pod-shaping ones.
// Saves are stubbed to echo the config back without writing the seeded config.
test.describe('manage dialog redeploy banner scoping (#460)', () => {
  // Workers run one test at a time and beforeEach resets this, so it cannot leak.
  let saveDelayMs = 0;

  test.beforeEach(async ({ page }) => {
    saveDelayMs = 0;
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as {
        method: string;
        args: unknown[];
      };
      if (body.method === 'SaveEnvironmentConfig') {
        if (saveDelayMs > 0) {
          await new Promise((resolve) => setTimeout(resolve, saveDelayMs));
        }
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
    // Three save round trips can approach the default 30s under contention.
    test.setTimeout(60_000);
    await openFirstEnvManageDialog(app);
    await app.manageDialog.selectTab('Runtime');

    await app.manageDialog.autoUpgradeCheckbox().click();
    await app.manageDialog.save();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);
    await expect(app.manageDialog.redeployBanner()).toHaveCount(0, withTestBudget());

    const idle = app.manageDialog.idleTimeoutInput();
    const original = await idle.inputValue();
    await idle.fill(original === '7m' ? '9m' : '7m');
    await app.manageDialog.save();
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible(withTestBudget());

    // A later metadata-only save must not clear a redeploy still owed.
    await app.manageDialog.autoUpgradeCheckbox().click();
    await app.manageDialog.save();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible(withTestBudget());

    // The stubbed saves never wrote the real config, so closing needs no restore.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The hold exceeds expect's 10s default while staying inside the declared 60s.
  test('a save round-trip slower than the expect default still converges on the declared budget', async ({
    app,
  }) => {
    test.setTimeout(60_000);
    saveDelayMs = 12_000;

    await openFirstEnvManageDialog(app);
    await app.manageDialog.selectTab('Runtime');

    await app.manageDialog.autoUpgradeCheckbox().click();
    await app.manageDialog.save();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

// Mouse row clicks can be intercepted by the env hover-card popover.
async function openFirstEnvManageDialog(app: AppShell): Promise<void> {
  await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
  await app.manageDialog.waitForOpen();
}
