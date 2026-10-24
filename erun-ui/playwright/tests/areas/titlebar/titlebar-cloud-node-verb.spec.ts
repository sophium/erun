import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The verb comes from the operation in flight, not the node's state (a start
// reports `running` before it finishes), and busy state is per environment.
// AWS calls are intercepted; the harness must never fire a real mutation.

interface InvokeBody {
  method: string;
  args: unknown[];
}

interface NodeFixture {
  cloudContextName: string;
  cloudContextStatus: string;
}

function managedIdleStatus(node: NodeFixture): unknown {
  return {
    timeoutSeconds: 600,
    secondsUntilStop: 500,
    stopEligible: true,
    outsideWorkingHours: false,
    managedCloud: true,
    fromPod: true,
    cloudContextName: node.cloudContextName,
    cloudContextStatus: node.cloudContextStatus,
    cloudContextLabel: node.cloudContextName,
    markers: [],
  };
}

function envelope(data: unknown): { contentType: string; body: string } {
  return { contentType: 'application/json', body: JSON.stringify({ data }) };
}

function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

// Waits on a real poll to prove a label survives it.
async function waitForNextIdlePoll(page: Page): Promise<void> {
  await page.waitForResponse(
    (response) =>
      response.url().includes('/__erun_invoke') &&
      (response.request().postData() ?? '').includes('"LoadIdleStatus"'),
  );
}

function selectionOf(body: InvokeBody): { tenant?: string; environment?: string } {
  const first: unknown = body.args[0];
  if (typeof first !== 'object' || first === null) {
    return {};
  }
  return first;
}

test.describe('titlebar cloud-node verb', () => {
  test('a start in flight never announces "Stopping", even once the node reports running', async ({
    app,
    page,
  }) => {
    const ctxName = 'mock-node-start';
    const node: NodeFixture = { cloudContextName: ctxName, cloudContextStatus: 'stopped' };
    const { promise: startHeld, resolve: releaseStart } = deferred();

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(envelope(managedIdleStatus(node)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        return route.fulfill(
          envelope({ name: ctxName, stopProtection: false, stopProtectionKnown: true }),
        );
      }
      if (body.method === 'StartCloudContext') {
        // The instance reports `running` before the start call returns.
        node.cloudContextStatus = 'running';
        await startHeld;
        return route.fulfill(envelope({ name: ctxName, status: 'running' }));
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    const startButton = app.titlebar.cloudNodePowerButton(new RegExp(`^Start ${ctxName}`));
    await expect(startButton).toBeVisible();
    await startButton.click();

    const pill = app.titlebar.idleTransitionPill();
    await expect(pill).toBeVisible();
    await expect(pill).toContainText(`Starting ${ctxName}`);
    // Surviving a poll proves the verb comes from the operation, not the state.
    await waitForNextIdlePoll(page);
    await expect(pill).toBeVisible();
    await expect(pill).toContainText(`Starting ${ctxName}`);
    await expect(pill).not.toContainText('Stopping');

    // Release the held RPC so the route handler does not leak into teardown.
    releaseStart();
  });

  test('an operation on one environment leaves another environment on its idle label', async ({
    app,
    page,
  }) => {
    const alphaNode = 'mock-node-alpha';
    const betaNode = 'mock-node-beta';
    const nodes: Record<string, NodeFixture> = {
      [SEED_ENV_ALPHA]: { cloudContextName: alphaNode, cloudContextStatus: 'stopped' },
      [SEED_ENV_BETA]: { cloudContextName: betaNode, cloudContextStatus: 'running' },
    };
    const { promise: startHeld, resolve: releaseStart } = deferred();

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        const environment = selectionOf(body).environment ?? '';
        const node = nodes[environment];
        if (!node) {
          return route.fulfill(envelope(null));
        }
        return route.fulfill(envelope(managedIdleStatus(node)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        const name = typeof body.args[0] === 'string' ? body.args[0] : '';
        return route.fulfill(envelope({ name, stopProtection: false, stopProtectionKnown: true }));
      }
      if (body.method === 'StartCloudContext') {
        await startHeld;
        return route.fulfill(envelope({ name: alphaNode, status: 'running' }));
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    const startButton = app.titlebar.cloudNodePowerButton(new RegExp(`^Start ${alphaNode}`));
    await expect(startButton).toBeVisible();
    await startButton.click();
    const pill = app.titlebar.idleTransitionPill();
    await expect(pill).toBeVisible();
    await expect(pill).toContainText(alphaNode);

    // Nothing is in flight against beta's node.
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_BETA);
    const betaStop = app.titlebar.cloudNodePowerButton(new RegExp(`^Stop ${betaNode}`));
    await expect(betaStop).toBeVisible();
    await waitForNextIdlePoll(page);
    await expect(pill).toBeHidden();
    await expect(betaStop).toBeEnabled();

    releaseStart();
  });
});
