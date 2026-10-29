import type { Page } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import {
  removeEnvironment,
  seedEnvironmentForK3d,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../fixtures/seedRoot.js';

// Opt-in (ERUN_E2E_K3D=1): the full create -> deploy -> open flow on a real cluster.

// `environment-initialized` runs the real create handler.
async function emitWailsEvent(page: Page, name: string, payload: unknown): Promise<void> {
  await page.evaluate(
    ({ name, payload }) => {
      (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime.EventsEmit(name, payload);
    },
    { name, payload },
  );
}

test.describe('k3d e2e: create → deploy → open (#647)', () => {
  test('create composes a real build→push→deploy and opens the env on k3d', async ({ app }) => {
    test.setTimeout(20 * 60 * 1000);

    const cluster = readK3dCluster();
    const tenant = SEED_TENANT;
    const environment = uniqueEnvironmentName('k3d');
    // No runtime version, so create must build and deploy a fresh one.
    seedEnvironmentForK3d(tenant, environment, cluster.context, cluster.registry);

    try {
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      await app.activityDrawer.open();
      await expect(app.activityDrawer.locator()).toContainText(/Building|Pushing|Deploying/, {
        timeout: 5 * 60 * 1000,
      });
      // The ERun tab appears only once the real pod is Ready and MCP binds.
      await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
        timeout: 15 * 60 * 1000,
      });
      await expect(app.sidebar.envOpenDot(tenant, environment)).toBeVisible();
    } finally {
      // The cluster itself is torn down in global-teardown.
      removeEnvironment(tenant, environment);
    }
  });
});
