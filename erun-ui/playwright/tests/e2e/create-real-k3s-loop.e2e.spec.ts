import type { Page, Request } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import { removeEnvironment, SEED_TENANT, uniqueEnvironmentName } from '../../fixtures/seedRoot.js';

// End-to-end verification against the developer's REAL erun-k3s cluster
// (ERUN_E2E_REAL_CLUSTER=erun-k3s): a "Skip Git checkout" create (noGit) — the
// control that avoids the interactive git remote-worktree flow — must compose
// EXACTLY ONE deploy and bring the runtime up (ERun tab binds), with no redeploy
// loop over a multi-minute observation window. This is the path we tell operators
// to use to sidestep the git-create loop, so it must genuinely work on the live
// cluster.

function deployMethodOf(request: Request): string | null {
  const body = request.postData() ?? '';
  for (const method of [
    'StartInitialDeploySession',
    'StartForceDeploySession',
    'StartCreateVersionSession',
    'StartDeploySession',
  ]) {
    if (body.includes(method)) {
      return method;
    }
  }
  return null;
}

test.describe('real erun-k3s e2e: Skip-Git create works and deploys exactly once', () => {
  test('a Skip-Git remote-agent create deploys once and comes up', async ({ app }) => {
    test.setTimeout(16 * 60 * 1000);

    const cluster = readK3dCluster();
    const tenant = SEED_TENANT;
    const environment = uniqueEnvironmentName('skipgit');

    const deploys: Array<{ method: string; at: number }> = [];
    const start = Date.now();
    app.page.on('request', (request) => {
      if (!request.url().includes('/__erun_invoke')) {
        return;
      }
      const method = deployMethodOf(request);
      if (method) {
        deploys.push({ method, at: Date.now() - start });
      }
    });

    try {
      const result = await app.page.evaluate(
        async (sel) => {
          const boundApp = (
            window as unknown as {
              go?: { main?: { App?: { StartInitSession?: (s: unknown, c: number, r: number) => unknown } } };
            }
          ).go?.main?.App;
          if (!boundApp?.StartInitSession) {
            return { ok: false, error: 'StartInitSession not exposed' };
          }
          try {
            return { ok: true, result: await boundApp.StartInitSession(sel, 120, 40) };
          } catch (e) {
            return { ok: false, error: String(e) };
          }
        },
        {
          tenant,
          environment,
          type: 'remote-agent',
          version: '1.0.149',
          runtimeImage: 'ghcr.io/sophium/erun-devops',
          kubernetesContext: cluster.context,
          clusterRegistry: true,
          noGit: true,
          setDefaultTenant: false,
        },
      );
      // eslint-disable-next-line no-console
      console.log('StartInitSession =>', JSON.stringify(result));
      expect(result.ok, `StartInitSession failed: ${JSON.stringify(result)}`).toBe(true);

      // The runtime comes up: the ERun tab appears only once the pod is Ready and
      // its MCP port-forward binds — proving the create fully succeeded.
      await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
        timeout: 8 * 60 * 1000,
      });

      // Observe past several redeploy cadences: a Skip-Git create must NOT loop.
      await app.page.waitForTimeout(5 * 60 * 1000);

      // eslint-disable-next-line no-console
      console.log('DEPLOY CALLS (skip-git):', JSON.stringify(deploys, null, 2));
      expect(
        deploys.length,
        `Skip-Git create must deploy exactly once, got ${deploys.length}: ${JSON.stringify(deploys)}`,
      ).toBe(1);
    } finally {
      removeEnvironment(tenant, environment);
    }
  });
});
