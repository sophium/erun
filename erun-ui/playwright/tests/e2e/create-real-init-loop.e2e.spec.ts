import type { Page, Request } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import { removeEnvironment, SEED_TENANT, uniqueEnvironmentName } from '../../fixtures/seedRoot.js';

// Reproduces the field create→deploy loop by driving the REAL init flow the
// desktop dialog uses — StartInitSession runs `erun init`, which deploys once and
// emits "==> Initialized", which the frontend's handleEnvironmentInitialized turns
// into a deploy, then opens. The earlier e2e emitted environment-initialized
// directly and so never exercised the real init path where the loop lives. A
// `runtime` env needs no git host / SSH, so this runs headless on k3d. Fails if
// more than one deploy is ever issued.

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

async function callStartInit(page: Page, selection: Record<string, unknown>): Promise<void> {
  await page.evaluate((sel) => {
    const app = (
      window as unknown as {
        go?: { main?: { App?: { StartInitSession?: (s: unknown, c: number, r: number) => unknown } } };
      }
    ).go?.main?.App;
    if (app?.StartInitSession) {
      void app.StartInitSession(sel, 120, 40);
      return;
    }
    // Fallback to the raw bridge if the generated binding isn't exposed.
    void fetch('/__erun_invoke', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ method: 'StartInitSession', args: [sel, 120, 40] }),
    });
  }, selection);
}

test.describe('k3d e2e: real init create issues exactly one deploy (no loop)', () => {
  test('a real runtime `erun init` create composes exactly one deploy', async ({ app }) => {
    test.setTimeout(14 * 60 * 1000);

    const cluster = readK3dCluster();
    const tenant = SEED_TENANT;
    const environment = uniqueEnvironmentName('realinit');

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
      await callStartInit(app.page, {
        tenant,
        environment,
        type: 'runtime',
        version: '1.0.149',
        runtimeImage: 'ghcr.io/sophium/erun-devops',
        kubernetesContext: cluster.context,
        containerRegistry: 'ghcr.io/sophium',
        setDefaultTenant: false,
      });

      // Wait for the first deploy the create composes.
      await expect(async () => {
        expect(deploys.length).toBeGreaterThanOrEqual(1);
      }).toPass({ timeout: 5 * 60 * 1000 });

      // Observe past several redeploy cadences to catch the loop.
      await app.page.waitForTimeout(5 * 60 * 1000);

      // eslint-disable-next-line no-console
      console.log('DEPLOY CALLS (real-init):', JSON.stringify(deploys, null, 2));
      expect(
        deploys.length,
        `real init must compose exactly one deploy, got ${deploys.length}: ${JSON.stringify(deploys)}`,
      ).toBe(1);
    } finally {
      removeEnvironment(tenant, environment);
    }
  });
});
