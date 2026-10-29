import type { Page } from '@playwright/test';

import { expect, test, waitForSeededRow, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  SEED_ORCHESTRATOR,
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// A POM convergence step must use its calling test's deadline, never its own
// `toPass({ timeout })` cap. Each case holds an RPC just past such a cap.

// holdInvoke delays every call to `method` until the window ends, so a
// repeated poll cannot slip a fast answer between slow ones.
async function holdInvoke(page: Page, method: string, holdMs: number): Promise<void> {
  const until = Date.now() + holdMs;
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === method) {
      const remaining = until - Date.now();
      if (remaining > 0) {
        await new Promise((resolve) => setTimeout(resolve, remaining));
      }
    }
    await route.continue();
  });
}

test.describe('shared helper convergence budgets (#2459)', () => {
  // The hold opens before seeding, so it needs cap + 3s rather than cap + 1s.
  test('a seeded row the backend reports at 33s is waited out on the test budget', async ({
    app,
    page,
  }, testInfo) => {
    test.setTimeout(60_000);
    const environment = uniqueEnvironmentName(testInfo.title);
    await holdInvoke(page, 'LoadState', 33_000);
    seedEnvironment(SEED_TENANT, environment);
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      await expect(app.sidebar.envRowButton(SEED_TENANT, environment)).toBeVisible();
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });
});

test.describe('sidebar POM convergence steps (#2459)', () => {
  test('a hover row that lands at 21s is waited out rather than cut off at the step cap', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await holdInvoke(page, 'ListOrchestrators', 21_000);
    await app.reboot();
    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    await expect(app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR)).toBeVisible();
  });

  test('an orchestrator dialog whose details button lands at 26s is not cut off at the step cap', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await holdInvoke(page, 'ListOrchestrators', 26_000);
    await app.reboot();
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await expect(app.page.getByRole('dialog', { name: 'Edit orchestrator' })).toBeVisible();
  });

  test('a hover-card read that outlasts the step cap is bounded by the test instead', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await holdInvoke(page, 'ListOrchestrators', 26_000);
    await app.reboot();
    // The read returns only once the card is up; a separate visibility assertion
    // would re-race the poll's re-render drop with nothing re-hovering.
    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible({ timeout: 2_000 });
    });
  });

  test('a close that the backend takes 31s to answer is waited out, not cut off', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);
    // isOpen follows StartSession asynchronously, so this wait uses the test budget.
    await expect(app.sidebar.envOpenDot(tenant, environment)).toHaveCount(1, withTestBudget());
    await holdInvoke(page, 'CloseEnvironmentSessions', 31_000);
    await app.sidebar.closeEnvironment(tenant, environment);
    // No re-assertion: a re-check would race tab spawns the open left in flight.
  });
});
