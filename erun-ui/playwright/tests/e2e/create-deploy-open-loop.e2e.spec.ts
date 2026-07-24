import type { Page, Request } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import { readK3dCluster } from '../../fixtures/k3dCluster.js';
import {
  removeEnvironment,
  seedRemoteAgentForK3d,
  seedRuntimeForK3d,
  SEED_TENANT,
  uniqueEnvironmentName,
} from '../../fixtures/seedRoot.js';

// Regression for the post-init redeploy loop: the desktop create -> deploy ->
// open flow must issue the runtime deploy EXACTLY ONCE. The observed bug redeploys
// on a ~90s cadence (each helm upgrade rolls the pod, the MCP port-forward drops,
// the desktop reads the runtime as down and redeploys — forever) even though the
// pod is healthy. This drives the real flow against a live k3d cluster, installing
// the published erun-devops runtime by reference from ghcr, and fails if a second
// deploy is ever issued. Two env types run because the loop was field-observed on
// remote-agent; runtime is the clean-baseline control.

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

async function assertSingleDeploy(
  app: { page: Page },
  seed: (tenant: string, environment: string, context: string) => void,
  label: string,
): Promise<void> {
  const cluster = readK3dCluster();
  const tenant = SEED_TENANT;
  const environment = uniqueEnvironmentName(label);
  seed(tenant, environment, cluster.context);

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
    }).toPass({ timeout: 3 * 60 * 1000 });

    await expect(app.page.getByRole('tab', { name: 'ERun', exact: true })).toBeVisible({
      timeout: 6 * 60 * 1000,
    });

    // Observe past two redeploy cadences after the first deploy to catch a loop.
    await app.page.waitForTimeout(4 * 60 * 1000);

    // eslint-disable-next-line no-console
    console.log(`DEPLOY CALLS (${label}):`, JSON.stringify(deploys, null, 2));
    expect(
      deploys.length,
      `expected exactly one deploy for ${label}, got ${deploys.length}: ${JSON.stringify(deploys)}`,
    ).toBe(1);
  } finally {
    removeEnvironment(tenant, environment);
  }
}

test.describe('k3d e2e: create → deploy → open issues exactly one deploy (no redeploy loop)', () => {
  test('runtime env: a single create composes exactly one deploy', async ({ app }) => {
    test.setTimeout(12 * 60 * 1000);
    await assertSingleDeploy(app, seedRuntimeForK3d, 'loop-runtime');
  });

  test('remote-agent env: a single create composes exactly one deploy', async ({ app }) => {
    test.setTimeout(12 * 60 * 1000);
    await assertSingleDeploy(app, seedRemoteAgentForK3d, 'loop-remote');
  });
});
