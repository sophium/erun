import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Two mirror-image defects on the Manage dialog's Runtime tab: the "Pending
// redeploy" banner fired on every save, including saves that only touched
// fields the running pod never sees (autoUpgrade/upgradeChannel select a
// future `erun upgrade` run; autoStart is desktop open-time behaviour),
// prompting a pointless pod roll — while editing those same fields never lit
// the per-tab unsaved-changes dot. The invariant: the dot reflects every
// editable field on the tab, the banner only pod-shaping ones.
//
// Saves are stubbed to echo the submitted config back, so the dialog runs a
// full save cycle without ever writing the seeded config.
test.describe('manage dialog redeploy banner scoping (#460)', () => {
  // Latency the stub below holds a save for before answering it, in
  // milliseconds. Zero for every test but the one that constructs a slow
  // round-trip on purpose; beforeEach resets it, and Playwright runs one test
  // at a time in a worker, so a value cannot leak from one test into another.
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
    // Three separate save round-trips, each with its own dot-clearing poll
    // and (for two of them) a banner convergence -- their combined legitimate
    // cost under contention can approach the default 30s test budget before
    // any single step is individually slow enough to look like a bug.
    test.setTimeout(60_000);
    await openFirstEnvManageDialog(app);
    await app.manageDialog.selectTab('Runtime');

    await app.manageDialog.autoUpgradeCheckbox().click();
    await app.manageDialog.save();
    // Dot clearing confirms the save round-tripped, so the no-banner check is meaningful.
    // withTestBudget throughout this test, not expect's 10s default: it
    // declared 60s precisely because these three round-trips are slow under
    // contention (see the comment on the budget above).
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);
    await expect(app.manageDialog.redeployBanner()).toHaveCount(0, withTestBudget());

    // Idle timeout is a pod-shaping field, so this save must fire the banner.
    const idle = app.manageDialog.idleTimeoutInput();
    const original = await idle.inputValue();
    await idle.fill(original === '7m' ? '9m' : '7m');
    await app.manageDialog.save();
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible(withTestBudget());

    // A later metadata-only save must not clear a redeploy the user still
    // owes the pod.
    await app.manageDialog.autoUpgradeCheckbox().click();
    await app.manageDialog.save();
    await expect
      .poll(() => app.manageDialog.tabHasUnsavedChanges('Runtime'), withTestBudget())
      .toBe(false);
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployBanner()).toBeVisible(withTestBudget());

    // The stubbed saves never wrote the real config, so closing needs no
    // restore.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The contention class this suite keeps paying for, constructed
  // deterministically rather than waited for on a loaded builder: a save
  // round-trip that outlasts Playwright's `expect.timeout` default (10s on
  // POSIX — playwright.config.ts) while sitting well inside the 60s this test
  // declares.
  //
  // Without `withTestBudget` the dot poll below is bounded by that 10s default,
  // so the step reds with "Timeout 10000ms exceeded" while 50s of the declared
  // budget goes unused — a step that is merely slow, not wrong, failing the
  // gate. The latency is this spec's own, held on the route handler the
  // beforeEach above already installs, so the case behaves identically on a
  // quiet machine and a throttled one.
  test('a save round-trip slower than the expect default still converges on the declared budget', async ({
    app,
  }) => {
    test.setTimeout(60_000);
    // Outlasts expect's 10s default, with the rest of the scenario still well
    // inside the declared 60s.
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

// Uses the keyboard path because mouse row clicks can be intercepted by the
// env hover-card popover on crowded sidebars.
async function openFirstEnvManageDialog(app: AppShell): Promise<void> {
  await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
  await app.manageDialog.waitForOpen();
}
