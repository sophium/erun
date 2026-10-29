import type { Request, Route } from '@playwright/test';

import { expect, test } from '../../fixtures/erunApp.js';
import {
  SEED_ENV_ALPHA,
  SEED_ORCHESTRATOR,
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../fixtures/seedRoot.js';

// One fast test per area, run on every build so a change breaks the gate even
// in an area the diff didn't touch.

test.describe('smoke', () => {
  test('sidebar: env rows render and a hover card opens', async ({ app }) => {
    await expect(app.sidebar.envRowButton(SEED_TENANT, SEED_ENV_ALPHA)).toBeVisible();
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA)).toBeVisible();
  });

  test('manage: the manage dialog opens and cancels', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.locator()).toBeVisible();
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('tenant: the tenant dashboard opens and shows its tabs', async ({ app, page }) => {
    const environment = uniqueEnvironmentName('smoke-tenant');
    seedEnvironment(SEED_TENANT, environment, 'apiurl: http://127.0.0.1:1/unreachable\n');
    try {
      await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
        const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
        if (body.method === 'LoadTenantDashboard') {
          await route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({ data: {} }),
          });
          return;
        }
        await route.continue();
      });
      await app.reloadEnvironments();
      await app.sidebar.openTenantDashboard(SEED_TENANT);
      await app.tenantDashboard.waitForOpen();
      await expect(app.tenantDashboard.tab('Audit log')).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('terminal: opening an environment renders a usable terminal pane', async ({ app }) => {
    await app.openEnvironmentTerminal(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(app.terminalPane.screen()).toBeVisible();
  });

  test('orchestrator: the edit dialog opens and cancels', async ({ app }) => {
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');
    await expect(app.orchestratorDialog.locator('Edit orchestrator')).toBeVisible();
    await app.orchestratorDialog.cancel('Edit orchestrator');
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });

  test('titlebar: the contribute toggle appears for an eligible environment', async ({
    app,
    page,
  }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    const toggle = page.getByRole('button', {
      name: /Contribute to ERun|Disable contribute mode/i,
    });
    // waitFor converges on the test's budget, not expect's.
    await toggle.first().waitFor({ state: 'visible' });
    await expect(toggle.first()).toBeVisible();
  });

  test('activity: the activity queue drawer opens and closes', async ({ app }) => {
    await app.activityDrawer.open();
    await expect(app.activityDrawer.locator()).toBeVisible();
    await app.activityDrawer.close();
    await expect(app.activityDrawer.locator()).toBeHidden();
  });

  test('review: the review panel toggles open and closed', async ({ app, page }) => {
    const splitter = page.getByRole('slider', { name: 'Resize diff panel' });
    const initiallyVisible = await splitter.isVisible().catch(() => false);

    // waitFor converges on the test's budget, not expect's.
    await app.titlebar.toggleReviewPanel();
    await splitter.waitFor({ state: initiallyVisible ? 'hidden' : 'visible' });
    await expect(splitter).toBeVisible({ visible: !initiallyVisible });

    // Restore so later tests in this worker don't inherit an open panel.
    await app.titlebar.toggleReviewPanel();
    await splitter.waitFor({ state: initiallyVisible ? 'visible' : 'hidden' });
  });

  test('deploy: the new-environment dialog opens and cancels', async ({ app }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();
    await expect(app.envInitDialog.tenantInput()).toBeVisible();
    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('diagnostics: the diagnostics console opens with the erun trace tab', async ({ app }) => {
    // A running orchestrator from an earlier spec outranks the environment
    // context, so establish it explicitly.
    await app.openEnvironmentTerminal(SEED_TENANT, SEED_ENV_ALPHA);

    await app.debugPanel.toggle();
    await app.debugPanel.waitForOpen();
    await expect(app.debugPanel.tab('erun trace')).toBeVisible();

    await app.debugPanel.toggle();
    await app.debugPanel.waitForClosed();
  });

  test('a11y: the terminal host is a named, reachable group', async ({ app }) => {
    await app.openEnvironmentTerminal(SEED_TENANT, SEED_ENV_ALPHA);
    const host = app.terminalPane.host();
    await expect(host).toBeVisible();
    await expect(host).toHaveAttribute('role', 'group');
    await expect(host).toHaveAccessibleName('Terminal');
  });

  test('platform: the global config dialog opens and cancels', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await expect(app.globalConfigDialog.locator()).toBeVisible();
    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });

  test('shell: the theme toggle switches the dark class', async ({ app, page }) => {
    const toggle = app.titlebar.themeToggleButton();
    await expect(toggle).toBeVisible();
    const wasDark = (await app.documentElement().getAttribute('class'))?.includes('dark') ?? false;

    // No locator-level class wait exists, so use waitForFunction.
    await app.titlebar.toggleTheme();
    await page.waitForFunction(
      (dark) => document.documentElement.classList.contains('dark') === dark,
      !wasDark,
    );
    await expect(app.documentElement()).toHaveClass(wasDark ? /^(?!.*dark).*$/ : /dark/);

    // Restore so later tests in this worker don't inherit the theme.
    await app.titlebar.toggleTheme();
    await page.waitForFunction(
      (dark) => document.documentElement.classList.contains('dark') === dark,
      wasDark,
    );
  });
});
