import { execFileSync } from 'node:child_process';

import type { Request } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import {
  kubeconfigPath,
  removeEnvironment,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../fixtures/seedRoot.js';

// Fault-injection reproduction of the field redeploy loop: on the erun-k3s (WSL2)
// cluster the MCP port-forward keeps dropping while the pod stays healthy, and the
// desktop reacts by redeploying — forever. k3d's port-forward is reliable, so this
// spec MANUFACTURES the same condition by repeatedly rolling the runtime pod (each
// roll drops the forward to the now-terminating pod, exactly like the field). The
// desktop's automatic reconnect runs `erun open --no-shell` (no --deploy), which
// must NEVER redeploy: a dropped forward against an already-deployed env issues no
// deploy. Fails (naming the culprit method) if it does.
//
// The env is a runtime (remote-worktree) type, so `erun init` deploys it once and
// the desktop opens directly — there is no frontend deploy at all. Any deploy call
// observed here is therefore an unwanted reconnect-triggered redeploy.

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

test.describe('k3d e2e: a dropped MCP port-forward must not trigger a redeploy', () => {
  test('rolling the runtime pod after deploy issues no further deploy', async ({ app }) => {
    test.setTimeout(15 * 60 * 1000);

    const cluster = readK3dCluster();
    const tenant = SEED_TENANT;
    const environment = uniqueEnvironmentName('mcpfault');
    const namespace = `${tenant}-${environment}`;

    const kubectlEnv = { ...process.env, KUBECONFIG: kubeconfigPath() };
    const rollPod = (): void => {
      try {
        execFileSync(
          'kubectl',
          [
            '--context',
            cluster.context,
            '-n',
            namespace,
            'delete',
            'pods',
            '--all',
            '--wait=false',
          ],
          { env: kubectlEnv, stdio: 'pipe' },
        );
      } catch {
        /* pod may be mid-roll; ignore */
      }
    };
    // Each roll is bounded by the cluster's own rollout completing rather than
    // by a guessed sleep: the condition this spec manufactures is "the forward
    // dropped and the pod came back", and that is exactly what rollout status
    // reports.
    const waitForRuntimeRollout = (): void => {
      execFileSync(
        'kubectl',
        [
          '--context',
          cluster.context,
          '-n',
          namespace,
          'rollout',
          'status',
          `deployment/${tenant}-devops`,
          '--timeout=5m',
        ],
        { env: kubectlEnv, stdio: 'pipe' },
      );
    };

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
      // Stand the env up the real way: `erun init` for a runtime env deploys the
      // runtime itself and the desktop opens directly (no frontend deploy).
      await app.page.evaluate(
        (sel) => {
          const boundApp = (
            window as unknown as {
              go?: {
                main?: {
                  App?: { StartInitSession?: (s: unknown, c: number, r: number) => unknown };
                };
              };
            }
          ).go?.main?.App;
          return boundApp?.StartInitSession?.(sel, 120, 40);
        },
        {
          tenant,
          environment,
          type: 'runtime',
          version: '1.0.149',
          runtimeImage: 'ghcr.io/sophium/erun-devops',
          kubernetesContext: cluster.context,
          containerRegistry: 'ghcr.io/sophium',
          setDefaultTenant: false,
        },
      );

      // Init's deploy + the direct open completes (real ghcr pull + helm rollout +
      // MCP bind); the ERun tab binds only once the pod is Ready.
      await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
        timeout: 8 * 60 * 1000,
      });
      const deploysAfterOpen = deploys.length;

      // Now manufacture the field condition: roll the pod nine times, waiting
      // each time for it to come back, so the desktop repeatedly sees the MCP
      // forward drop against a healthy, already-deployed env.
      for (let roll = 0; roll < 9; roll += 1) {
        rollPod();
        waitForRuntimeRollout();
      }

      console.log('DEPLOY CALLS (mcp-fault):', JSON.stringify(deploys, null, 2));
      expect(
        deploys,
        `a dropped MCP forward must not redeploy: ${deploys.length - deploysAfterOpen} extra deploy(s) after open — ${JSON.stringify(deploys)}`,
      ).toHaveLength(deploysAfterOpen);
    } finally {
      try {
        execFileSync(
          'helm',
          ['--kube-context', cluster.context, '-n', namespace, 'uninstall', `${tenant}-devops`],
          { stdio: 'ignore' },
        );
        execFileSync(
          'kubectl',
          ['--context', cluster.context, 'delete', 'ns', namespace, '--wait=false'],
          {
            stdio: 'ignore',
          },
        );
      } catch {
        /* ignore cleanup failures */
      }
      removeEnvironment(tenant, environment);
    }
  });
});
