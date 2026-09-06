import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  addOrchestrators,
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// erun#1748: with a realistic population (the field report: 7 orchestrators,
// 9+ environments spread across the operator's real tenants), the whip
// popover's target list fills its own 40vh scroll region, and the primary
// action used to sit below that scroll region -- reachable only by scrolling
// the whole panel. This spec builds that population directly (rather than
// depending on whatever another spec in this worker happened to leave
// behind) so the assertions hold regardless of run order, and proves: the
// action is reachable with no scroll, it still carries the selected-target
// count, and the three select-all controls stay on one row at that scale.

const ENVIRONMENT_COUNT = 9;
const ORCHESTRATOR_COUNT = 7;

// whipCountText mirrors Titlebar.WhipAction.tsx's own whipButtonLabel: the
// pluralization the primary action renders for a given selected count.
function whipCountText(count: number): string {
  return `Whip ${String(count)} target${count === 1 ? '' : 's'}`;
}

// whipCheckedCount counts however many target rows the popover renders
// checked right now. The suite's baseline seeds a default tenant/environment
// (`defaulttenant: pw`, pw's `defaultenvironment: alpha`), so a fresh boot's
// sidebar focus -- and therefore Whip's preselected default target -- is not
// guaranteed to be empty; backend-side sessions persisting across specs in
// the same worker (playwright/AGENTS.md) mean the exact starting selection
// is not under this spec's control either (mirrors titlebar-whip-action.spec
// .ts's own whipCountLabel helper, built for the same reason). Reading the
// real starting count, rather than assuming zero, is how this spec proves a
// single manual check widens the selection by exactly one without depending
// on ambient state it does not own.
async function whipCheckedCount(app: import('../../../pages/index.js').AppShell): Promise<number> {
  return app.titlebar.whipPanel().locator('[role="checkbox"][aria-checked="true"]').count();
}

test('a realistic population keeps the whip action reachable without scrolling', async ({
  app,
}, testInfo) => {
  // This spec's seed is far larger than the default single-row case the
  // suite's global 30s per-test timeout is tuned for (root AGENTS.md's "no
  // flaky tests" gate needs this to be a real budget increase, not a race
  // against the whole-test clock the widened waitForSeededRow call below
  // would still lose).
  test.setTimeout(90_000);
  const tenant = uniqueEnvironmentName(testInfo.title);
  const firstEnvironment = 'env-0';
  const lastEnvironment = `env-${String(ENVIRONMENT_COUNT - 1)}`;
  const environments = Array.from({ length: ENVIRONMENT_COUNT }, (_, i) => `env-${String(i)}`);
  seedTenant(tenant, firstEnvironment);
  for (const environment of environments) {
    seedEnvironment(tenant, environment);
  }
  const orchestratorIds = Array.from(
    { length: ORCHESTRATOR_COUNT },
    (_, i) => `${tenant}-orch-${String(i)}`,
  );
  const restoreOrchestrators = addOrchestrators(orchestratorIds, tenant, firstEnvironment);

  try {
    // WhipTargets reads the config tree fresh on every call (no reload/wait
    // needed), but the sidebar itself only picks up new rows via fsnotify --
    // reload it so the population is visibly staged before driving the panel.
    // This spec's seed (9 environments plus 7 orchestrators written in one
    // batch) is far larger than any other waitForSeededRow caller's, so a
    // reload here genuinely costs more to resolve and render -- widen the
    // budget rather than share the single-row default.
    await waitForSeededRow(app, tenant, lastEnvironment, 60_000);

    await app.titlebar.whipButton().click();

    // Tick one target near the top of the now-long list -- the reported
    // click -- and the primary action must already be reachable, with no
    // scroll of any kind performed to find it. This freshly seeded target
    // cannot already be checked, so whatever the popover started with, one
    // manual check must widen it by exactly one.
    const firstTarget = `${tenant}/${firstEnvironment}`;
    await expect(app.titlebar.whipTargetCheckbox(firstTarget)).not.toBeChecked();
    const startingChecked = await whipCheckedCount(app);
    await app.titlebar.whipTargetCheckbox(firstTarget).check();
    await expect(app.titlebar.whipRunButton()).toBeVisible();
    await expect(app.titlebar.whipRunButton()).toHaveText(whipCountText(startingChecked + 1));

    // Structural proof, not just a visual one: the action is a sibling of the
    // scrollable target list, never a descendant of it, so target count can
    // never push it below the fold again.
    const scrollRegion = app.titlebar.whipPanel().locator('.overflow-y-auto').first();
    await expect(scrollRegion.getByRole('button', { name: /^Whip \d+ target/ })).toHaveCount(0);

    // The three select-all controls occupy one row even at this population --
    // the specific reported wrapping failure was two-then-one across three
    // rows. Same top y-coordinate is a direct assertion on the layout, not
    // merely that three buttons exist.
    const orchestratorsBox = await app.titlebar.selectAllOrchestratorsButton().boundingBox();
    const environmentsBox = await app.titlebar.selectAllEnvironmentsButton().boundingBox();
    const allBox = await app.titlebar.selectAllButton().boundingBox();
    expect(orchestratorsBox).not.toBeNull();
    expect(environmentsBox).not.toBeNull();
    expect(allBox).not.toBeNull();
    expect(Math.abs((orchestratorsBox?.y ?? 0) - (environmentsBox?.y ?? 0))).toBeLessThan(2);
    expect(Math.abs((orchestratorsBox?.y ?? 0) - (allBox?.y ?? 0))).toBeLessThan(2);

    // Each icon control still resolves by its own accessible label -- a bare,
    // unlabeled icon would fail these very locators (Titlebar.ts's
    // selectAll*Button() methods query getByRole('button', { name: ... })).
    await expect(app.titlebar.selectAllOrchestratorsButton()).toBeVisible();
    await expect(app.titlebar.selectAllEnvironmentsButton()).toBeVisible();
    await expect(app.titlebar.selectAllButton()).toBeVisible();

    // Reported visually -- captured visually. animations: 'disabled' finishes
    // the popover's own open transition before capturing, so the shot is
    // never a frozen mid-fade frame.
    await app.page.screenshot({
      path: 'test-results/titlebar-whip-panel-layout-realistic-population.png',
      animations: 'disabled',
    });
  } finally {
    restoreOrchestrators();
    removeTenant(tenant);
  }
});
