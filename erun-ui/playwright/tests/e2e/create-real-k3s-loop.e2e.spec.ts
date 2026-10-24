import { execSync } from 'node:child_process';

import type { Page, Request } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import { removeEnvironment, SEED_TENANT, uniqueEnvironmentName } from '../../fixtures/seedRoot.js';

// Against the real erun-k3s cluster (ERUN_E2E_REAL_CLUSTER=erun-k3s): a noGit
// create must come up with exactly one Helm revision, since `erun init` owns
// the deploy. helm history is checked because init deploys in a subprocess
// the frontend cannot observe.

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

// observeIdlePolls bounds the observation by the app's own polls; a wedged app
// stops producing them and fails here.
async function observeIdlePolls(page: Page, count: number): Promise<void> {
  for (let poll = 0; poll < count; poll += 1) {
    await page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('LoadIdleStatus'),
      { timeout: 60_000 },
    );
  }
}

test.describe('real erun-k3s e2e: Skip-Git create comes up with one Helm revision', () => {
  test('a Skip-Git remote-agent create deploys once (via init) and comes up', async ({ app }) => {
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
              go?: {
                main?: {
                  App?: { StartInitSession?: (s: unknown, c: number, r: number) => unknown };
                };
              };
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
      console.log('StartInitSession =>', JSON.stringify(result));
      expect(result.ok, `StartInitSession failed: ${JSON.stringify(result)}`).toBe(true);

      await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
        timeout: 8 * 60 * 1000,
      });

      // The pod-rolling redeploy landed ~68s after install; ~150 polls cover it.
      await observeIdlePolls(app.page, 150);

      console.log('DEPLOY CALLS (skip-git):', JSON.stringify(deploys, null, 2));
      expect(
        deploys,
        `desktop must compose no post-init deploy (init owns it), got ${deploys.length}: ${JSON.stringify(deploys)}`,
      ).toHaveLength(0);

      // Ground truth: exactly one Helm revision.
      const namespace = `${tenant}-${environment}`;
      const release = `${tenant}-devops`;
      const historyJSON = execSync(
        `helm --kube-context ${cluster.context} -n ${namespace} history ${release} -o json`,
        { encoding: 'utf8' },
      );
      const revisions = JSON.parse(historyJSON) as unknown[];
      console.log('HELM REVISIONS:', historyJSON);
      expect(
        revisions,
        `create must produce exactly one Helm revision (a 2nd = the pod-rolling redeploy), got ${revisions.length}`,
      ).toHaveLength(1);
    } finally {
      try {
        execSync(
          `helm --kube-context ${cluster.context} -n ${tenant}-${environment} uninstall ${tenant}-devops`,
          {
            stdio: 'ignore',
          },
        );
        execSync(
          `kubectl --context ${cluster.context} delete ns ${tenant}-${environment} --wait=false`,
          {
            stdio: 'ignore',
          },
        );
      } catch {
        // ignore cleanup failures
      }
      removeEnvironment(tenant, environment);
    }
  });
});
