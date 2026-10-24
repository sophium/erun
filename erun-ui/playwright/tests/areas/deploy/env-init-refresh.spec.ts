import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { parseInvoke } from '../../../pages/index.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The init handler's retry loop alone runs ~19s, so the scenario budget covers it with margin.
const ENV_INIT_ERROR_BUDGET_MS = 45_000;
const SCENARIO_MARGIN_MS = 30_000;
const SCENARIO_BUDGET_MS = ENV_INIT_ERROR_BUDGET_MS + SCENARIO_MARGIN_MS;

// RELOAD_ATTEMPTS must match ENVIRONMENT_INIT_RELOAD_ATTEMPTS in wailsEventThunks.ts.
// Eight holds of this length push the loop past the default 30s budget.
const LOAD_STATE_HOLD_MS = 2_000;
const RELOAD_ATTEMPTS = 8;

const SLOW_RELOAD_BUDGET_MS = ENV_INIT_ERROR_BUDGET_MS + RELOAD_ATTEMPTS * LOAD_STATE_HOLD_MS;
const SLOW_SCENARIO_BUDGET_MS = SLOW_RELOAD_BUDGET_MS + SCENARIO_MARGIN_MS;

// The kubectl stub fails namespace-ensure, so a real init never emits this event.
async function emitWailsEvent(page: Page, name: string, payload?: unknown): Promise<void> {
  await page.evaluate(
    ({ name, payload }) => {
      const runtime = (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime;
      if (payload === undefined) {
        runtime.EventsEmit(name);
      } else {
        runtime.EventsEmit(name, payload);
      }
    },
    { name, payload },
  );
}

test.describe('environment init refresh', () => {
  test('environment-initialized surfaces a brand-new tenant row and confirms with a toast', async ({
    app,
  }) => {
    // Only the init handler shows the success toast (the fsnotify reload doesn't).
    const tenant = uniqueEnvironmentName('init-tenant');
    const environment = 'local';
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      // Freeze the clock so the transient success icon survives until asserted.
      await app.page.clock.install();
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      await expect(app.titlebar.messageCenterIcon('success')).toBeVisible(withTestBudget());
      await expect(app.sidebar.envRowButton(tenant, environment)).toBeVisible(withTestBudget());
    } finally {
      removeTenant(tenant);
    }
  });

  test('environment-initialized surfaces a recoverable error when the env never appears', async ({
    app,
  }) => {
    test.setTimeout(SCENARIO_BUDGET_MS);
    // An env never written to disk drives the handler's retry-then-error path.
    const tenant = uniqueEnvironmentName('ghost-tenant');
    const environment = 'local';
    await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

    await expect(app.titlebar.messageCenterIcon('error')).toBeVisible({
      timeout: ENV_INIT_ERROR_BUDGET_MS,
    });
    await app.titlebar.openMessageCenter('error');
    await expect(app.titlebar.messageCenterRow('did not appear in the sidebar')).toBeVisible();
    await expect(app.sidebar.envRowButton(tenant, environment)).toHaveCount(0);
  });

  test('an exhausted init retry loop still reports when every reload is slow', async ({
    app,
    page,
  }) => {
    test.setTimeout(SLOW_SCENARIO_BUDGET_MS);
    // Holding each reload forces the loop past 30s deterministically.
    const tenant = uniqueEnvironmentName('slow-ghost-tenant');
    const environment = 'local';

    await page.route('**/__erun_invoke', async (route) => {
      if (parseInvoke(route.request())?.method === 'LoadState') {
        // Deliberate stimulus, not a wait for the app.
        await new Promise<void>((resolve) => setTimeout(resolve, LOAD_STATE_HOLD_MS));
      }
      await route.continue();
    });

    await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });
    await expect(app.titlebar.messageCenterIcon('error')).toBeVisible({
      timeout: SLOW_RELOAD_BUDGET_MS,
    });
    await app.titlebar.openMessageCenter('error');
    await expect(app.titlebar.messageCenterRow('did not appear in the sidebar')).toBeVisible();
  });

  // LoadState is held just past expect's 10s default; the confirmation must still be
  // waited out on the test's own budget.
  test('a confirmation that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    const tenant = uniqueEnvironmentName('slow-success-tenant');
    const environment = 'local';
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      let heldOnce = false;
      await page.route('**/__erun_invoke', async (route, request) => {
        const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
        if (body.method === 'LoadState' && !heldOnce) {
          heldOnce = true;
          await new Promise((resolve) => setTimeout(resolve, 12_000));
        }
        await route.continue();
      });

      // Freeze the clock so the transient success icon survives until asserted.
      await app.page.clock.install();
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      await expect(app.titlebar.messageCenterIcon('success')).toBeVisible(withTestBudget());
    } finally {
      removeTenant(tenant);
    }
  });
});
