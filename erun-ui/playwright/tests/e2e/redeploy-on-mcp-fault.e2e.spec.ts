import { execFileSync } from 'node:child_process';

import type { Page, Request } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import {
  kubeconfigPath,
  removeEnvironment,
  seedRuntimeForK3d,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../fixtures/seedRoot.js';

// Fault-injection reproduction of the field redeploy loop: on the erun-k3s (WSL2)
// cluster the MCP port-forward keeps dropping while the pod stays healthy, and the
// desktop reacts by redeploying — forever. k3d's port-forward is reliable, so this
// spec MANUFACTURES the same condition by repeatedly rolling the runtime pod (each
// roll drops the forward to the now-terminating pod, exactly like the field). An
// automatic deploy must be one-shot: a dropped forward against an already-deployed
// env must never trigger another deploy. Fails (naming the culprit method) if it does.

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
    seedRuntimeForK3d(tenant, environment, cluster.context);

    const kubectlEnv = { ...process.env, KUBECONFIG: kubeconfigPath() };
    const rollPod = (): void => {
      try {
        execFileSync(
          'kubectl',
          ['--context', cluster.context, '-n', namespace, 'delete', 'pods', '--all', '--wait=false'],
          { env: kubectlEnv, stdio: 'pipe' },
        );
      } catch {
        /* pod may be mid-roll; ignore */
      }
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
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      // First deploy + open completes (real ghcr pull + helm rollout + MCP bind).
      await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
        timeout: 8 * 60 * 1000,
      });
      const deploysAfterOpen = deploys.length;

      // Now manufacture the field condition: roll the pod every ~20s for ~3
      // minutes so the desktop repeatedly sees the MCP forward drop against a
      // healthy, already-deployed env.
      for (let i = 0; i < 9; i += 1) {
        rollPod();
        await app.page.waitForTimeout(20_000);
      }

      // eslint-disable-next-line no-console
      console.log('DEPLOY CALLS (mcp-fault):', JSON.stringify(deploys, null, 2));
      expect(
        deploys.length,
        `a dropped MCP forward must not redeploy: ${deploys.length - deploysAfterOpen} extra deploy(s) after open — ${JSON.stringify(deploys)}`,
      ).toBe(deploysAfterOpen);
    } finally {
      removeEnvironment(tenant, environment);
    }
  });
});
