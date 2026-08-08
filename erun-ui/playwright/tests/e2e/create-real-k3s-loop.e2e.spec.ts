import { execSync } from 'node:child_process';

import type { Page, Request } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import { removeEnvironment, SEED_TENANT, uniqueEnvironmentName } from '../../fixtures/seedRoot.js';

// End-to-end verification against the developer's REAL erun-k3s cluster
// (ERUN_E2E_REAL_CLUSTER=erun-k3s): a "Skip Git checkout" create (noGit) — the
// control that avoids the interactive git remote-worktree flow — must bring the
// runtime up (ERun tab binds) with EXACTLY ONE cluster Helm revision and no
// desktop-composed post-init deploy. `erun init` owns the env's single deploy;
// the desktop composing its own deploy afterwards was the bug that rolled the
// just-created pod (a second Helm revision). We assert the ground truth (one
// revision) via helm history, not just the frontend call count, because the
// init deploy runs inside the `erun init` subprocess where a frontend-only
// counter is blind to it.

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

// observeIdlePolls holds the "nothing happened" window open on the desktop's own
// activity instead of the wall clock: every completed idle poll is a real
// round-trip the app performs while it is live, so a count of them bounds the
// observation by an event — and an app that wedges stops producing them and
// fails here rather than letting the assertion pass vacuously.
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

      // The runtime comes up: the ERun tab appears only once the pod is Ready and
      // its MCP port-forward binds — proving the create fully succeeded.
      await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
        timeout: 8 * 60 * 1000,
      });

      // Observe past a couple of redeploy cadences: the pod-rolling redeploy
      // landed ~68s after the install, so ~150 idle polls (the desktop's own
      // ~1s cadence) covers it with room to spare.
      await observeIdlePolls(app.page, 150);

      // init (`erun init`) owns the env's single runtime deploy, so the desktop
      // must compose NO deploy of its own — a post-init redeploy is exactly the
      // bug that rolled the just-created pod.
      console.log('DEPLOY CALLS (skip-git):', JSON.stringify(deploys, null, 2));
      expect(
        deploys,
        `desktop must compose no post-init deploy (init owns it), got ${deploys.length}: ${JSON.stringify(deploys)}`,
      ).toHaveLength(0);

      // Ground truth: exactly ONE Helm revision on the cluster. A second revision
      // is the double-deploy that rolled the pod. helm is real + on PATH in e2e
      // mode (ERUN_E2E_K3D=1 stubs only aws).
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
      // Best-effort cluster cleanup so reruns start clean; config removal too.
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
