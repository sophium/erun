import type { Page, Request, Route } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The gateway is rendered at deploy time, so a settings change needs a
// redeploy. The suite has no cluster, so routing reads are stubbed.

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
        // Recorded rather than run: there is no cluster to deploy to.
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

    // Otherwise the absence passes merely because the check had not finished.
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
    await app.manageDialog.waitForRedeployBanner();
    await expect(app.manageDialog.redeployNowButton()).toBeVisible();
  });
});
