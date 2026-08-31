import { expect, test } from '../fixtures/erunApp.js';
import {
  addOrchestrators,
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

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

test('a realistic population keeps the whip action reachable without scrolling', async ({
  app,
}, testInfo) => {
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
    await app.reloadEnvironments();
    await app.sidebar
      .envRowButton(tenant, lastEnvironment)
      .waitFor({ state: 'visible', timeout: 30_000 });

    await app.titlebar.whipButton().click();

    // Tick one target near the top of the now-long list -- the reported
    // click -- and the primary action must already be reachable, with no
    // scroll of any kind performed to find it.
    const firstTarget = `${tenant}/${firstEnvironment}`;
    await app.titlebar.whipTargetCheckbox(firstTarget).check();
    await expect(app.titlebar.whipRunButton()).toBeVisible();
    await expect(app.titlebar.whipRunButton()).toHaveText('Whip 1 target');

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
