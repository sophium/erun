import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// #1204: the tenant dashboard, an orchestrator's session, and an environment's
// session each used to compute their own "active"/"selected" sidebar
// highlight from a different state slice, with nothing enforcing that only
// one applied. Opening an orchestrator while the tenant dashboard was open
// attached its session but never showed it (the dashboard stayed painted
// over the terminal pane) while both rows rendered selected at once. The fix
// is a single derived focus value every row reads from, and this spec
// exercises all three pairings of the invariant it restores: at most one of
// the tenant dashboard, an orchestrator row, and an environment row is ever
// the sidebar's focused row, and the main pane always agrees with it.

const RUNNING_SESSION_ID = 4242;

function runningOrchestratorSnapshot() {
  return {
    id: SEED_ORCHESTRATOR,
    name: SEED_ORCHESTRATOR,
    environments: [],
    tenants: [],
    directories: [],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    transient: false,
  };
}

// Stubs ListOrchestrators so the seeded orchestrator reads as already
// running with a fixed session id — clicking its row then dispatches the
// synchronous "open" path (openOrchestrator), not a real orchestrator spawn,
// which the isolated harness has no credentials to perform.
async function stubRunningOrchestrator(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [runningOrchestratorSnapshot()] }),
      });
    }
    await route.continue();
  });
}

function tenantDashboardHeading(app: { page: Page }) {
  return app.page.getByRole('heading', { level: 1, name: SEED_TENANT, exact: true });
}

test.describe('sidebar focus is mutually exclusive across dashboard, orchestrator, and environment (#1204)', () => {
  test('opening a running orchestrator while the tenant dashboard is open replaces it and swaps the highlight', async ({
    app,
    page,
  }) => {
    await stubRunningOrchestrator(page);
    await app.reboot();

    await app.sidebar.openTenantDashboard(SEED_TENANT);
    await expect(tenantDashboardHeading(app)).toBeVisible();
    await expect(app.sidebar.tenantDashboardButton(SEED_TENANT)).toHaveAttribute(
      'aria-current',
      'page',
    );

    await app.sidebar.openOrchestratorSession(SEED_ORCHESTRATOR);

    // The dashboard is gone — not just deselected in the sidebar — and the
    // orchestrator's session is what the pane actually shows instead.
    await expect(tenantDashboardHeading(app)).toHaveCount(0);
    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    await expect(app.tabStrip.environmentMode()).toBeHidden();

    // Exactly one row is highlighted.
    await expect(app.sidebar.tenantDashboardButton(SEED_TENANT)).not.toHaveAttribute(
      'aria-current',
      'page',
    );
    await expect(app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR)).toHaveAttribute(
      'aria-current',
      'page',
    );
  });

  test('opening the tenant dashboard while an orchestrator is focused replaces it and swaps the highlight', async ({
    app,
    page,
  }) => {
    await stubRunningOrchestrator(page);
    await app.reboot();

    await app.sidebar.openOrchestratorSession(SEED_ORCHESTRATOR);
    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    await expect(app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR)).toHaveAttribute(
      'aria-current',
      'page',
    );

    await app.sidebar.openTenantDashboard(SEED_TENANT);

    await expect(tenantDashboardHeading(app)).toBeVisible();
    await expect(app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR)).not.toHaveAttribute(
      'aria-current',
      'page',
    );
    await expect(app.sidebar.tenantDashboardButton(SEED_TENANT)).toHaveAttribute(
      'aria-current',
      'page',
    );
  });

  test('opening an environment while an orchestrator is focused replaces it and swaps the highlight', async ({
    app,
    page,
  }) => {
    await stubRunningOrchestrator(page);
    await app.reboot();

    await app.sidebar.openOrchestratorSession(SEED_ORCHESTRATOR);
    await expect(app.tabStrip.orchestratorMode()).toBeVisible();

    await app.openEnvironmentTerminal(SEED_TENANT, SEED_ENV_ALPHA);

    await expect(app.tabStrip.environmentMode()).toBeVisible();
    await expect(app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR)).not.toHaveAttribute(
      'aria-current',
      'page',
    );
    await expect(app.sidebar.envRowButton(SEED_TENANT, SEED_ENV_ALPHA)).toHaveAttribute(
      'aria-current',
      'page',
    );
  });
});
