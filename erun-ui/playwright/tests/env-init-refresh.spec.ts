import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// emitWailsEvent fires a backend event into the headless bridge, the same way
// env-init.spec drives 'environments-changed'. The desktop's PTY trace
// handler emits 'environment-initialized' (with a {tenant, environment}
// payload) after observing `==> Initialized <tenant>/<env>` in the Local
// shell; firing it here exercises handleEnvironmentInitialized directly,
// which the real `erun init` flow cannot reach in this harness (the kubectl
// stub fails namespace-ensure, so a live local-agent init never completes).
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
    // Reproduces the reported scenario: `erun init` creates a brand-new
    // tenant + env, then the desktop's init-complete signal must make it
    // appear in the sidebar. The config is written first (so the handler's
    // reload sees it), then the targeted event fires. The success toast is
    // the handler-only signal — the fsnotify watcher's reload surfaces the
    // row but shows no toast — so asserting the toast proves
    // handleEnvironmentInitialized ran to completion, not just that the row
    // appeared by some other path.
    const tenant = uniqueEnvironmentName('init-tenant');
    const environment = 'local';
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      // Assert the transient success toast first — it auto-dismisses after a
      // few seconds, whereas the row is persistent.
      await expect(app.titlebar.statusMessage()).toContainText(
        `Created ${tenant} / ${environment}`,
        { timeout: 10_000 },
      );
      await expect(app.sidebar.envRowButton(tenant, environment)).toBeVisible({ timeout: 10_000 });
    } finally {
      removeTenant(tenant);
    }
  });

  test('environment-initialized surfaces a recoverable error when the env never appears', async ({
    app,
  }) => {
    // Regression guard for the silent-stale-sidebar bug: a transient reload
    // miss (best-effort and swallowed by reloadStateAfterEnvironmentChange)
    // used to leave the sidebar stale with no feedback at all. The handler
    // now retries and, when the env still never surfaces, raises a
    // recoverable error instead of nothing (Nielsen #1 + #9). Firing the
    // event for an env that was never written drives the
    // bounded-retry-then-observe path deterministically: nothing is on disk,
    // so the watcher never surfaces it either, and the old code path would
    // have shown no toast.
    const tenant = uniqueEnvironmentName('ghost-tenant');
    const environment = 'local';
    await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

    await expect(app.titlebar.statusMessage()).toContainText('did not appear in the sidebar', {
      timeout: 15_000,
    });
    await expect(app.sidebar.envRowButton(tenant, environment)).toHaveCount(0);
    await app.titlebar.dismissStatus();
  });
});
