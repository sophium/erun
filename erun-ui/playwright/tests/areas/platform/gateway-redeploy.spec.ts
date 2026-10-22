import type { Page, Request, Route } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The gateway is rendered into each runtime at deploy time, so switching it in
// ERun settings changes no running pod by itself — the operator's report was
// that switching it off "does not have the desired effect". The contract here:
// after a save the desktop names the environments still routed the old way and
// redeploys them on request, and an environment's Manage dialog says its pod is
// behind its saved configuration.
//
// What a running pod routes to is read from the cluster, which the inert suite
// has none of, so the routing reads are answered here. Reading a real
// Deployment is covered by erun-common TestReadGatewayRouting* and the
// per-environment selection by erun-ui TestLoadGatewayRoutingDrift*.

const OLD_GATEWAY = 'https://openrouter.ai/api';

interface RoutingStubs {
  driftReads: number;
  deploys: unknown[];
}

function stubGatewayRouting(page: Page, drifted: boolean): RoutingStubs {
  const stubs: RoutingStubs = { driftReads: 0, deploys: [] };
  const item = {
    tenant: SEED_TENANT,
    environment: SEED_ENV_ALPHA,
    deployed: OLD_GATEWAY,
    wanted: '',
  };
  void page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string; args?: unknown[] };
    switch (body.method) {
      case 'LoadGatewayRoutingDrift':
        stubs.driftReads += 1;
        await route.fulfill({ json: { data: drifted ? [item] : [] } });
        return;
      case 'LoadEnvironmentGatewayRouting':
        await route.fulfill({
          json: {
            data: drifted ? item : { tenant: '', environment: '', deployed: '', wanted: '' },
          },
        });
        return;
      case 'StartDeploySession':
        // Recorded rather than run: the inert suite has no cluster to deploy to,
        // and what matters is which environment the confirmation redeploys.
        stubs.deploys.push((body.args ?? [])[0]);
        await route.fulfill({ json: { data: { sessionId: 'gateway-redeploy-stub', slot: 0 } } });
        return;
      default:
        await route.continue();
    }
  });
  return stubs;
}

test.describe('applying a gateway switch to running environments', () => {
  test('offers the redeploy a switched-off gateway needs, and runs it', async ({ app, page }) => {
    const stubs = stubGatewayRouting(page, true);
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.save();
    await app.globalConfigDialog.waitForClosed();

    await app.gatewayRedeployDialog.waitForOpen();
    const row = app.gatewayRedeployDialog.row(SEED_ENV_ALPHA);
    await expect(row).toContainText(`Gateway ${OLD_GATEWAY}`);
    await expect(row).toContainText('Own Claude sign-in');

    await app.gatewayRedeployDialog.redeploy();
    await app.gatewayRedeployDialog.waitForClosed();
    await expect
      .poll(() => stubs.deploys, withTestBudget())
      .toEqual([{ tenant: SEED_TENANT, environment: SEED_ENV_ALPHA }]);
  });

  test('offers nothing when every running environment already matches', async ({ app, page }) => {
    const stubs = stubGatewayRouting(page, false);
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.save();
    await app.globalConfigDialog.waitForClosed();

    // Converge on the read having been answered before asserting the absence,
    // or the assertion passes merely because the check had not finished.
    await expect.poll(() => stubs.driftReads, withTestBudget()).toBeGreaterThan(0);
    await expect(app.gatewayRedeployDialog.locator()).toHaveCount(0);
    expect(stubs.deploys).toEqual([]);
  });

  test('leaves the redeploy to the operator when they choose Later', async ({ app, page }) => {
    const stubs = stubGatewayRouting(page, true);
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await app.globalConfigDialog.save();
    await app.globalConfigDialog.waitForClosed();

    await app.gatewayRedeployDialog.waitForOpen();
    await app.gatewayRedeployDialog.later();
    await app.gatewayRedeployDialog.waitForClosed();
    expect(stubs.deploys).toEqual([]);
  });

  test('the Manage dialog says a pod is still on the old routing', async ({ app, page }) => {
    stubGatewayRouting(page, true);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    // Raised without a save in this dialog: the switch was made elsewhere, and
    // the pod is what is behind.
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployNowButton()).toBeVisible();
  });
});
