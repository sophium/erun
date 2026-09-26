import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

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

      // The confirmation is a state transition (the init handler's reload
      // landing, then the toast), so it converges on the element reaching
      // visible under this test's own budget rather than expect's 10s default
      // -- see the held-reload case at the end of this file for the
      // reproduction.
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
      await app.titlebar.messageCenterIcon('success').waitFor({ state: 'visible' });
      await deployStarted;
      await expect(erunTab).toHaveCount(0);
    } finally {
      removeTenant(tenant);
    }
  });

  // The confirmation icon is what the init handler's reload loop, and then its
  // toast, produce -- a state transition owned by an async read rather than a
  // render that has already happened. Asserting it with an explicit 10s bound
  // capped the step below the 30s this test declares, so a reload that is
  // merely slow reds the test with two thirds of its own clock unspent. The
  // hold below is deliberately just past that cap: the smallest delay that
  // discriminates, so the suite pays seconds here rather than the tens a
  // genuinely loaded machine would. It is injected at LoadState -- the read
  // the handler waits on -- rather than by loading the machine, so the
  // reproduction is deterministic on a quiet host.
  //
  // Pre-fix this case reds at exactly 10_000ms with 50s of its own budget
  // unused; converging on the element's own state passes when the reload
  // actually lands.
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

      // Freeze the clock so the transient success icon can't auto-dismiss
      // before the assertion below observes it.
      await page.clock.install();
      await emitWailsEvent(page, 'environment-initialized', { tenant, environment });

      await expect(app.titlebar.messageCenterIcon('success')).toBeVisible(withTestBudget());
    } finally {
      removeTenant(tenant);
    }
  });
});
