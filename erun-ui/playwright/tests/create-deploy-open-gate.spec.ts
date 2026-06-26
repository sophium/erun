import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// Issue #644 — the create flow must compose a deploy before opening, and open
// the env's tabs only once the runtime is actually up. Before the fix,
// `handleEnvironmentInitialized` opened the env immediately after `erun init`,
// so the ERun/AI tabs spawned against a runtime that did not exist and failed
// with "timed out waiting for MCP port-forward". Now the handler composes a
// deploy (StartDeploySession) and records the env as pending-open; the matching
// `environment-deployed` signal opens the tabs.
//
// emitWailsEvent fires a backend event into the headless bridge (the same
// mechanism env-init-refresh.spec uses): the real `erun init` + build → push →
// deploy + runtime-pod flow cannot run in this inert harness (the kubectl/helm/
// docker stubs make a live deploy impossible), so the create→deploy→open *gate*
// is driven by firing the two lifecycle events directly. The full real
// create→deploy→open happy path against a live cluster is covered by the
// opt-in k3d e2e suite (#647); the per-env-type deploy decision is covered by
// deploy_orchestration_test.go and deploy-orchestration.spec.ts.
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
      // The create handler composes a deploy rather than opening directly. Wait
      // on the StartDeploySession round-trip so the assertions are bounded by a
      // real event, not a sleep.
      const deployStarted = app.page.waitForResponse(
        (response) =>
          response.url().includes('/__erun_invoke') &&
          (response.request().postData() ?? '').includes('StartDeploySession'),
      );
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      // The success toast confirms the handler ran to completion.
      await expect(app.titlebar.statusMessage()).toContainText(
        `Created ${tenant} / ${environment}`,
        { timeout: 10_000 },
      );
      // Create composed a deploy (the #644 fix: init used to open, not deploy).
      await deployStarted;
      // Gate: the env's tabs stay closed against the not-yet-deployed runtime.
      // The just-created env is now selected but has no live runtime, so no
      // ERun tab exists — the regression this spec guards.
      await expect(erunTab).toHaveCount(0);

      // The deploy lands → the gate releases → the env opens its tabs.
      await emitWailsEvent(app.page, 'environment-deployed', { tenant, environment });
      await expect(erunTab).toBeVisible({ timeout: 10_000 });
    } finally {
      removeTenant(tenant);
    }
  });

  test('a deploy signal with no pending-open entry does not open the env', async ({ app }) => {
    // The gate only opens an env the user just created and queued to open. A
    // deploy signal for any other env (the Deploy button, a manual redeploy)
    // must not spawn tabs behind the user's back. A deploy signal that arrives
    // before any pending entry exists is ignored; the env opens only once
    // create records it and its own deploy lands. The window is bounded by the
    // create handler's StartDeploySession round-trip, not a sleep.
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
      // The earlier deploy signal did not open the env — it is still gated,
      // waiting for the deploy the create handler just composed.
      await expect(erunTab).toHaveCount(0);
    } finally {
      removeTenant(tenant);
    }
  });
});
