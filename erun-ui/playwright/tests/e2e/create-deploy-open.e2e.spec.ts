import type { Page } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import {
  removeEnvironment,
  seedEnvironmentForK3d,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../fixtures/seedRoot.js';

// Opt-in k3d-backed end-to-end coverage (issue #647). Unlike the default inert
// suite — which PATH-prepends stub kubectl/helm/docker and pins ERUN_APP_CLL to
// an inert `erun` stub, so no real deploy ever runs — this spec drives the full
// desktop create → build → push → deploy → open → MCP flow against a REAL local
// k3d cluster + registry (created in global-setup). It is the coverage that
// would have caught the create-no-deploy regression (#644) end-to-end, which
// the inert harness structurally cannot see.
//
// GATED: skipped unless ERUN_E2E_K3D=1 (set only by `run.sh --e2e-k3d` on a host
// with Docker + k3d + binfmt). The default `run.sh` / `make integration-test`
// runs this file but it skips, so the default suite stays inert and offline.
//
// Determinism (no-flaky-tests, #643): every wait is on an observable condition
// (activity-queue trace lines, the rendered ERun tab) with a per-spec timeout
// sized for a real multi-arch build → push → deploy round-trip, never a sleep.

// emitWailsEvent fires a backend event into the headless bridge — the same seam
// env-init-refresh / create-deploy-open-gate use. Firing `environment-initialized`
// runs the #644 create handler, which composes the REAL build → push → deploy
// here (because the env below points at the live cluster) and opens the env once
// the matching `environment-deployed` signal lands.
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

// Collected only when ERUN_E2E_K3D=1 (playwright.config.ts testIgnore); the
// default inert suite never runs this directory.
test.describe('k3d e2e: create → deploy → open (#647)', () => {
  test('create composes a real build→push→deploy and opens the env on k3d', async ({ app }) => {
    // A multi-arch build + push + helm rollout against a real cluster is minutes,
    // far slower than any default spec — size the budget accordingly.
    test.setTimeout(20 * 60 * 1000);

    const cluster = readK3dCluster();
    const tenant = SEED_TENANT;
    const environment = uniqueEnvironmentName('k3d');
    // Seed the env `erun init` would have written: a fresh local-agent at the
    // live cluster + registry, with NO runtimeversion, so the create handler
    // must build → push → deploy a fresh version (the #644 path).
    seedEnvironmentForK3d(tenant, environment, cluster.context, cluster.registry);

    try {
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      // The composed deploy streams its umbrella lines into the activity drawer.
      await app.activityDrawer.open();
      await expect(app.activityDrawer.locator()).toContainText(/Building|Pushing|Deploying/, {
        timeout: 5 * 60 * 1000,
      });
      // Deploy succeeds → the runtime pod is Ready → the create gate opens the
      // env, so the ERun tab appears (its MCP port-forward bound to the real
      // pod; the inert-harness MCP-timeout bug cannot occur here).
      await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
        timeout: 15 * 60 * 1000,
      });
      // The runtime really came up on the cluster.
      await expect(app.sidebar.envOpenDot(tenant, environment)).toBeVisible();
    } finally {
      // Best-effort: remove the env config + uninstall its namespace from the
      // cluster so the shared k3d cluster does not accumulate releases across
      // specs. The cluster + registry themselves are torn down in global-teardown.
      removeEnvironment(tenant, environment);
    }
  });
});
