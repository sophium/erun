import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// A local-agent (builds-here) env is NOT deployed by `erun init`, so on
// environment-initialized the desktop composes the single build→push→deploy and
// opens the env's tabs only once the runtime is up (the matching
// environment-deployed signal): opening against a not-yet-deployed runtime fails
// with an MCP port-forward timeout — the regression this spec guards. The
// seeded envs here are local-agent (seedRoot: `type: local-agent`). A
// remote-worktree env is deployed by init itself and opens directly instead;
// that path is covered by the opt-in k3d e2e suite.
//
// The inert harness cannot run a live deploy (kubectl/helm/docker are stubbed),
// so the gate is exercised by firing the two lifecycle events directly.
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
      // The success confirmation and a later deploy failure now render as
      // independent icons (one per class) rather than sharing one pill slot,
      // so there is no "replaces within milliseconds" race to work around by
      // recording a stream — freezing the clock is enough to catch the
      // transient success icon before its own auto-dismiss.
      await app.page.clock.install();
      const deployStarted = app.page.waitForResponse(
        (response) =>
          response.url().includes('/__erun_invoke') &&
          (response.request().postData() ?? '').includes('StartInitialDeploySession'),
      );
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      await expect(app.titlebar.messageCenterIcon('success')).toBeVisible({ timeout: 10_000 });
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

      await app.page.clock.install();
      const deployStarted = app.page.waitForResponse(
        (response) =>
          response.url().includes('/__erun_invoke') &&
          (response.request().postData() ?? '').includes('StartInitialDeploySession'),
      );
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });
      await expect(app.titlebar.messageCenterIcon('success')).toBeVisible({ timeout: 10_000 });
      await deployStarted;
      await expect(erunTab).toHaveCount(0);
    } finally {
      removeTenant(tenant);
    }
  });
});
