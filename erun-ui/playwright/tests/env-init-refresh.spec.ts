import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// Fires the event directly because the real `erun init` flow cannot complete
// in this harness: the kubectl stub fails namespace-ensure, so a live
// local-agent init never reaches the point of emitting the init-complete signal.
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
    // tenant + env and the init-complete signal must surface it in the
    // sidebar. The success toast (a message centre icon, not a
    // pill) is the handler-only signal — the fsnotify watcher's reload
    // surfaces the row but shows no toast — so asserting the icon proves the
    // init handler ran, not that the row appeared by some other path.
    const tenant = uniqueEnvironmentName('init-tenant');
    const environment = 'local';
    seedTenant(tenant, environment);
    seedEnvironment(tenant, environment);
    try {
      // Freeze the clock so the transient success icon can't auto-dismiss
      // before the assertion below observes it.
      await app.page.clock.install();
      await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

      await expect(app.titlebar.messageCenterIcon('success')).toBeVisible({ timeout: 10_000 });
      await expect(app.sidebar.envRowButton(tenant, environment)).toBeVisible({ timeout: 10_000 });
    } finally {
      removeTenant(tenant);
    }
  });

  test('environment-initialized surfaces a recoverable error when the env never appears', async ({
    app,
  }) => {
    // Regression guard for the silent-stale-sidebar bug: a swallowed reload
    // miss used to leave the sidebar stale with no feedback. The handler now
    // retries and, when the env still never surfaces, raises a recoverable
    // error instead of nothing (Nielsen #1 + #9). Firing the event for an env
    // that was never written drives that path deterministically — nothing is
    // on disk, so the watcher never surfaces the row either.
    const tenant = uniqueEnvironmentName('ghost-tenant');
    const environment = 'local';
    await emitWailsEvent(app.page, 'environment-initialized', { tenant, environment });

    // The env never exists, so the handler exhausts its full retry budget
    // (8 attempts, a getInitialState round trip plus a 400ms delay each)
    // before raising the error -- on a loaded machine each round trip alone
    // can take longer than the 400ms delay between them, so the wait here
    // must clear the retry loop's own worst case with real margin, not just
    // the fast-path duration a lightly-loaded machine sees.
    await expect(app.titlebar.messageCenterIcon('error')).toBeVisible({ timeout: 45_000 });
    await app.titlebar.openMessageCenter('error');
    await expect(app.titlebar.messageCenterRow('did not appear in the sidebar')).toBeVisible();
    await expect(app.sidebar.envRowButton(tenant, environment)).toHaveCount(0);
  });
});
