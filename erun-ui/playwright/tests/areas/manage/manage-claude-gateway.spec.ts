import type { Request, Route } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Patches the real response so the spec cannot drift from what the backend sends.
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
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('AI');

    await expect(app.manageDialog.claudeGatewayField()).toHaveCount(0);
  });

  test('offers leaving the gateway, and nothing about the credential', async ({ app, page }) => {
    await withGatewayCatalog(page);
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('AI');

    await expect(app.manageDialog.claudeGatewayField()).toBeVisible();
    // The seeded row is the suite's baseline and must not be edited here.
    await expect(app.manageDialog.claudeGatewayField()).toContainText(/Default/);
    // The credential is an erun-level value delivered by deploy, so there is no field for it.
    await expect(app.manageDialog.claudeGatewaySecretInput()).toHaveCount(0);
  });

  for (const choice of ['Disabled', 'Enabled'] as const) {
    test(`choosing ${choice} sticks, marks the tab dirty, and is saved`, async ({ app, page }) => {
      await withGatewayCatalog(page);
      // Saves echo the submitted config back without writing the seeded config.
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
      await expect
        .poll(() => app.manageDialog.tabHasUnsavedChanges('AI'), withTestBudget())
        .toBe(true);
      await expect(app.manageDialog.claudeDefaultModelSelect()).toContainText(
        choice === 'Disabled' ? 'Default (opus)' : 'Default (vendor/model-x)',
      );

      await expect(app.manageDialog.claudeModelCheckbox('opus')).toHaveCount(
        choice === 'Disabled' ? 1 : 0,
      );

      await app.manageDialog.save();
      await expect.poll(() => saved.length, withTestBudget()).toBe(1);
      expect(saved[0]?.claude?.useGateway).toBe(choice === 'Enabled');
    });
  }
});
