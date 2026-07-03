import type { Page } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import {
  removeEnvironment,
  seedEnvironmentForK3d,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../fixtures/seedRoot.js';

// Opt-in k3d-backed e2e: drives the full desktop create → build → push → deploy
// → open → MCP flow against a REAL local cluster. This is the coverage that
// catches create-no-deploy regressions the inert stub harness structurally
// cannot see. Gated behind ERUN_E2E_K3D=1 so the default suite stays offline.

// Firing `environment-initialized` runs the backend create handler, which here
// composes the REAL build → push → deploy (the env points at the live cluster)
// and opens the env once the matching `environment-deployed` signal lands.
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
    // A real multi-arch build → push → deploy against a live cluster takes minutes.
    test.setTimeout(20 * 60 * 1000);

    const cluster = readK3dCluster();
    const tenant = SEED_TENANT;
    const environment = uniqueEnvironmentName('k3d');
    // Seed the env with NO runtime version, so the create handler is forced to
    // build → push → deploy a fresh version rather than reuse an existing one.
    seedEnvironmentForK3d(tenant, environment, cluster.context, cluster.registry);

    try {
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      await app.activityDrawer.open();
      await expect(app.activityDrawer.locator()).toContainText(/Building|Pushing|Deploying/, {
        timeout: 5 * 60 * 1000,
      });
      // The ERun tab appears only once the real runtime is Ready and its MCP
      // port-forward binds to the live pod — the path the inert harness cannot
      // verify, since it has no real pod for its MCP-timeout bug to surface.
      await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
        timeout: 15 * 60 * 1000,
      });
      await expect(app.sidebar.envOpenDot(tenant, environment)).toBeVisible();
    } finally {
      // Keep the shared k3d cluster clean so specs do not accumulate releases
      // across runs; the cluster + registry themselves are torn down in global-teardown.
      removeEnvironment(tenant, environment);
    }
  });
});
