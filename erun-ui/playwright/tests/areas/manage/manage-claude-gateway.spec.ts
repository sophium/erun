import type { Request, Route } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// withGatewayCatalog marks the environment's read as living under a configured
// erun-level gateway. It patches the real response rather than restating the
// read model's shape, so the spec cannot drift from what the backend sends.
async function withGatewayCatalog(page: import('@playwright/test').Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== 'LoadEnvironmentConfig') {
      await route.continue();
      return;
    }
    const response = await route.fetch();
    const payload = (await response.json()) as {
      data?: { claudeDefaults?: Record<string, unknown> };
    };
    if (payload.data?.claudeDefaults) {
      payload.data.claudeDefaults.gatewayConfigured = true;
      payload.data.claudeDefaults.gatewayModels = ['vendor/model-x'];
      payload.data.claudeDefaults.gatewayDefaultModel = 'vendor/model-x';
    }
    await route.fulfill({ response, json: payload });
  });
}

test.describe('per-environment gateway overrides', () => {
  test('stays out of the way when no gateway catalog exists', async ({ app }) => {
    // The baseline has no catalog, so an environment has nothing to override and
    // the controls are absent rather than offering a switch that changes nothing.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('AI');

    await expect(app.manageDialog.claudeGatewayField()).toHaveCount(0);
  });

  test('offers leaving the gateway, and nothing about the credential', async ({ app, page }) => {
    // The catalog is one erun-level list, so without this an environment could
    // only be moved off the gateway by moving every environment with it.
    await withGatewayCatalog(page);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('AI');

    await expect(app.manageDialog.claudeGatewayField()).toBeVisible();
    // Reading only: the seeded row is the suite's baseline and must not be
    // edited by a spec that is not about editing it.
    await expect(app.manageDialog.claudeGatewayField()).toContainText(/Default/);
    // The credential is one erun-level value delivered into this namespace by
    // deploy, so there is deliberately nothing here to name. A field for it
    // would be a second way to say what the catalog already says, and the wrong
    // one when the catalog is global.
    await expect(app.manageDialog.claudeGatewaySecretInput()).toHaveCount(0);
  });

  // The reported defect: choosing Disabled (or Enabled) snapped back to
  // Default, the tab never became dirty, and Save wrote nothing — so an
  // environment could not be taken off the gateway from the desktop at all.
  for (const choice of ['Disabled', 'Enabled'] as const) {
    test(`choosing ${choice} sticks, marks the tab dirty, and is saved`, async ({ app, page }) => {
      await withGatewayCatalog(page);
      // Saves are stubbed to echo the submitted config back, so the dialog runs
      // a full save cycle without writing the seeded config.
      const saved: { claude?: { useGateway?: boolean } }[] = [];
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = JSON.parse(request.postData() ?? '{}') as { method: string; args: unknown[] };
        if (body.method !== 'SaveEnvironmentConfig') {
          await route.fallback();
          return;
        }
        const config = body.args[1] as { claude?: { useGateway?: boolean } };
        saved.push(config);
        await route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: config }),
        });
      });
      await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
      await app.manageDialog.waitForOpen();
      await app.manageDialog.selectTab('AI');
      expect(await app.manageDialog.tabHasUnsavedChanges('AI')).toBe(false);

      await app.manageDialog.chooseClaudeGateway(choice);

      await expect(app.manageDialog.claudeGatewayField()).toContainText(choice);
      await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('AI')).toBe(true);
      // The model choices follow the gateway choice before the save: off the
      // gateway the environment launches its own models, on it the catalog's.
      await expect(app.manageDialog.claudeDefaultModelSelect()).toContainText(
        choice === 'Disabled' ? 'Default (opus)' : 'Default (vendor/model-x)',
      );

      await app.manageDialog.save();
      await expect.poll(() => saved.length).toBe(1);
      expect(saved[0]?.claude?.useGateway).toBe(choice === 'Enabled');
    });
  }
});
