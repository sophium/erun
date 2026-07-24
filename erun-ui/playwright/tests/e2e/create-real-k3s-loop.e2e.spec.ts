import type { Page, Request } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import {
  removeEnvironment,
  seedGitRemoteAgentForK3d,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../fixtures/seedRoot.js';

// Reproduces the field git-create redeploy loop against the developer's REAL
// erun-k3s cluster (ERUN_E2E_REAL_CLUSTER=erun-k3s), WITHOUT the per-env SSH-key
// import that blocks automating a real `erun init`. The SSH wait only gates init;
// the loop is in the post-init deploy→open cycle. So we seed the exact config a
// git create produces (crucially `localrepopath`), then fire environment-initialized
// — the same event the backend emits after a real init — and assert the create
// composes exactly ONE deploy. The instrumented erun-app records the trigger of any
// extra deploy (with a stack) to %APPDATA%\erun\loop-trigger.log.

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

test.describe('real erun-k3s e2e: git-create composes exactly one deploy (no loop)', () => {
  test('a git remote-agent create issues exactly one deploy', async ({ app }) => {
    test.setTimeout(16 * 60 * 1000);

    const cluster = readK3dCluster();
    const tenant = SEED_TENANT;
    const environment = uniqueEnvironmentName('gitloop');
    seedGitRemoteAgentForK3d(tenant, environment, cluster.context);

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

      await expect(async () => {
        expect(deploys.length).toBeGreaterThanOrEqual(1);
      }).toPass({ timeout: 6 * 60 * 1000 });

      // Observe past several redeploy cadences to catch the loop.
      await app.page.waitForTimeout(5 * 60 * 1000);

      // eslint-disable-next-line no-console
      console.log('DEPLOY CALLS (git-real-k3s):', JSON.stringify(deploys, null, 2));
      expect(
        deploys.length,
        `git create must compose exactly one deploy, got ${deploys.length}: ${JSON.stringify(deploys)}`,
      ).toBe(1);
    } finally {
      removeEnvironment(tenant, environment);
    }
  });
});
