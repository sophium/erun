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

// Rolling the runtime pod drops the MCP forward while the env stays deployed;
// the reconnect (`erun open --no-shell`) must never redeploy. A runtime env is
// deployed by `erun init` alone, so any frontend deploy call here is a defect.

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
    // Bounded by rollout status, not a sleep.
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

      await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
        timeout: 8 * 60 * 1000,
      });
      const deploysAfterOpen = deploys.length;

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
