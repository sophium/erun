import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// The create flow must compose a deploy and open the env's tabs only once the
// runtime is up: opening tabs against a not-yet-deployed runtime fails with an
// MCP port-forward timeout — the regression this spec guards.
//
// The inert harness cannot run a live deploy (kubectl/helm/docker are stubbed),
// so the gate is exercised by firing the two lifecycle events directly; the
// real happy path is covered by the opt-in k3d e2e suite.
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
      const deployStarted = app.page.waitForResponse(
        (response) =>
          response.url().includes('/__erun_invoke') &&
          (response.request().postData() ?? '').includes('StartDeploySession'),
      );
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      await expect(app.titlebar.statusMessage()).toContainText(
        `Created ${tenant} / ${environment}`,
        { timeout: 10_000 },
      );
      await deployStarted;
      await expect(erunTab).toHaveCount(0);

      await emitWailsEvent(app.page, 'environment-deployed', { tenant, environment });
      await expect(erunTab).toBeVisible({ timeout: 10_000 });
    } finally {
      removeTenant(tenant);
    }
  });

  test('a deploy signal with no pending-open entry does not open the env', async ({ app }) => {
    // The gate only opens an env the user just created and queued to open. A
    // deploy signal for any other env (the Deploy button, a manual redeploy)
    // must not spawn tabs behind the user's back.
    const tenant = uniqueEnvironmentName('nopending-tenant');
    const environment = 'local';
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    const erunTab = app.page.getByRole('tab', { name: 'ERun', exact: true });
    try {
      // No pending entry yet: this deploy signal must be a no-op.
      await emitWailsEvent(app.page, 'environment-deployed', { tenant, environment });

      const deployStarted = app.page.waitForResponse(
        (response) =>
          response.url().includes('/__erun_invoke') &&
          (response.request().postData() ?? '').includes('StartDeploySession'),
      );
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });
      await expect(app.titlebar.statusMessage()).toContainText(
        `Created ${tenant} / ${environment}`,
        { timeout: 10_000 },
      );
      await deployStarted;
      await expect(erunTab).toHaveCount(0);
    } finally {
      removeTenant(tenant);
    }
  });
});
