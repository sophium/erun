import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// A local-agent env opens only after environment-deployed; opening earlier fails with an
// MCP port-forward timeout. The inert harness can't deploy, so the events are fired directly.
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

test.describe('create → deploy → open gate (#644)', () => {
  test('init composes a deploy and gates the open until the env is deployed', async ({ app }) => {
    const tenant = uniqueEnvironmentName('gate-tenant');
    const environment = 'local';
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    const erunTab = app.page.getByRole('tab', { name: 'ERun', exact: true });
    try {
      // Freeze the clock so the transient success icon survives until asserted.
      await app.page.clock.install();
      const deployStarted = app.page.waitForResponse(
        (response) =>
          response.url().includes('/__erun_invoke') &&
          (response.request().postData() ?? '').includes('StartInitialDeploySession'),
      );
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      await app.titlebar.messageCenterIcon('success').waitFor({ state: 'visible' });
      await deployStarted;
      await expect(erunTab).toHaveCount(0);

      await emitWailsEvent(app.page, 'environment-deployed', { tenant, environment });
      await erunTab.waitFor({ state: 'visible' });
    } finally {
      removeTenant(tenant);
    }
  });

  test('a deploy signal with no pending-open entry does not open the env', async ({ app }) => {
    // A deploy signal for an env not pending open must not spawn tabs.
    const tenant = uniqueEnvironmentName('nopending-tenant');
    const environment = 'local';
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    const erunTab = app.page.getByRole('tab', { name: 'ERun', exact: true });
    try {
      await emitWailsEvent(app.page, 'environment-deployed', { tenant, environment });

      await app.page.clock.install();
      const deployStarted = app.page.waitForResponse(
        (response) =>
          response.url().includes('/__erun_invoke') &&
          (response.request().postData() ?? '').includes('StartInitialDeploySession'),
      );
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });
      await app.titlebar.messageCenterIcon('success').waitFor({ state: 'visible' });
      await deployStarted;
      await expect(erunTab).toHaveCount(0);
    } finally {
      removeTenant(tenant);
    }
  });

  // LoadState is held just past expect's 10s default; the confirmation must still be
  // waited out on the test's own budget.
  test('a create confirmation that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    const tenant = uniqueEnvironmentName('slow-gate-tenant');
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
      await page.clock.install();
      await emitWailsEvent(page, 'environment-initialized', { tenant, environment });

      await expect(app.titlebar.messageCenterIcon('success')).toBeVisible(withTestBudget());
    } finally {
      removeTenant(tenant);
    }
  });
});
