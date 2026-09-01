import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The titlebar's cloud-node control used to derive its progressive verb from the
// node's CURRENT state and its busy flag from one global boolean. Two false
// statements followed, and both are asserted here:
//
//   A. "Stopping <node>" during a start. The verb came from `running`, and a
//      start flips the node to running (AWS reports it before the cluster is
//      usable, and the idle poll picks that up) while the operation is still in
//      flight — so the pill announced a teardown that was not happening.
//
//   B. The message bleeding across environments. The node NAME followed the
//      selected environment while the busy flag did not, so selecting an
//      environment with nothing running still showed a progressive label.
//
// The harness has no managed cloud context and must never fire a real AWS
// mutation, so LoadIdleStatus and the power RPCs are intercepted and the
// lifecycle is simulated — the same approach idle-widget-stop-protection.spec.ts
// takes, for the same reason.

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

// A deterministic beat tied to a real poll round-trip, never the wall clock:
// used to prove a label SURVIVES a poll rather than merely rendering once.
async function waitForNextIdlePoll(page: Page): Promise<void> {
  await page.waitForResponse(
    (response) =>
      response.url().includes('/__erun_invoke') &&
      (response.request().postData() ?? '').includes('"LoadIdleStatus"'),
  );
}

// LoadIdleStatus is called with the selection, so a per-environment fixture has
// to read which environment the poll is for.
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
    // Starts stopped, which is what makes the button offer Start at all.
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
        // Production's own sequence: the instance reaches `running` well before
        // the start call returns (it still waits on Kubernetes access), and the
        // idle poll reports that mid-flight. This is the exact state that made
        // the old label read "Stopping" during a start.
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
    // The node has already flipped to running by now; a poll is what carries
    // that into the widget, so surviving one is what proves the verb comes from
    // the operation rather than the state.
    await waitForNextIdlePoll(page);
    await expect(pill).toBeVisible();
    await expect(pill).toContainText(`Starting ${ctxName}`);
    await expect(pill).not.toContainText('Stopping');

    // Release the held RPC or the route handler leaks into test teardown.
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

    // Switching to an environment on a different node: nothing is in flight
    // against beta's node, so beta's control must read its own idle label.
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_BETA);
    const betaStop = app.titlebar.cloudNodePowerButton(new RegExp(`^Stop ${betaNode}`));
    await expect(betaStop).toBeVisible();
    // Bound the "nothing happened" check by a real poll for beta, so the
    // assertion runs after a completed round-trip rather than a guessed delay.
    await waitForNextIdlePoll(page);
    await expect(pill).toBeHidden();
    await expect(betaStop).toBeEnabled();

    releaseStart();
  });
});
