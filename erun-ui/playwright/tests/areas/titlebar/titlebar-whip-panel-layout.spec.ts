import { artifactPath } from '../../../fixtures/artifacts.js';
import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  addOrchestrators,
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// With a realistic population the whip action must be reachable without
// scrolling and the select-all controls must stay on one row.

const ENVIRONMENT_COUNT = 9;
const ORCHESTRATOR_COUNT = 7;

// whipCountText mirrors Titlebar.WhipAction.tsx's whipButtonLabel.
function whipCountText(count: number): string {
  return `Whip ${String(count)} target${count === 1 ? '' : 's'}`;
}

// whipCheckedCount reads the starting selection, which ambient worker state
// and the default focus make non-zero.
async function whipCheckedCount(app: import('../../../pages/index.js').AppShell): Promise<number> {
  return app.titlebar.whipPanel().locator('[role="checkbox"][aria-checked="true"]').count();
}

test('a realistic population keeps the whip action reachable without scrolling', async ({
  app,
}, testInfo) => {
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
    // The sidebar picks up seeded rows only via fsnotify.
    await waitForSeededRow(app, tenant, lastEnvironment);

    await app.titlebar.openWhipPanel();

    const firstTarget = `${tenant}/${firstEnvironment}`;
    // Wait on the row with the test's budget; the population loads slowly.
    await app.titlebar.whipTargetCheckbox(firstTarget).waitFor({ state: 'visible' });
    await expect(app.titlebar.whipTargetCheckbox(firstTarget)).not.toBeChecked();
    const startingChecked = await whipCheckedCount(app);
    await app.titlebar.whipTargetCheckbox(firstTarget).check();
    await expect(app.titlebar.whipRunButton()).toBeVisible();
    await expect(app.titlebar.whipRunButton()).toHaveText(whipCountText(startingChecked + 1));

    // The action is a sibling of the scroll region, never inside it.
    const scrollRegion = app.titlebar.whipPanel().locator('.overflow-y-auto').first();
    await expect(scrollRegion.getByRole('button', { name: /^Whip \d+ target/ })).toHaveCount(0);

    // One row: same top y for all three.
    const orchestratorsBox = await app.titlebar.selectAllOrchestratorsButton().boundingBox();
    const environmentsBox = await app.titlebar.selectAllEnvironmentsButton().boundingBox();
    const allBox = await app.titlebar.selectAllButton().boundingBox();
    expect(orchestratorsBox).not.toBeNull();
    expect(environmentsBox).not.toBeNull();
    expect(allBox).not.toBeNull();
    expect(Math.abs((orchestratorsBox?.y ?? 0) - (environmentsBox?.y ?? 0))).toBeLessThan(2);
    expect(Math.abs((orchestratorsBox?.y ?? 0) - (allBox?.y ?? 0))).toBeLessThan(2);

    await expect(app.titlebar.selectAllOrchestratorsButton()).toBeVisible();
    await expect(app.titlebar.selectAllEnvironmentsButton()).toBeVisible();
    await expect(app.titlebar.selectAllButton()).toBeVisible();

    await app.page.screenshot({
      path: artifactPath('test-results/titlebar-whip-panel-layout-realistic-population.png'),
      animations: 'disabled',
    });
  } finally {
    restoreOrchestrators();
    removeTenant(tenant);
  }
});
