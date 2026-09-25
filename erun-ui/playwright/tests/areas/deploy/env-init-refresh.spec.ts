import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { parseInvoke } from '../../../pages/index.js';
import {
  removeTenant,
  seedEnvironment,
  seedTenant,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// The error case below waits out the init handler's own retry loop -- 8
// attempts, a state reload plus a 400ms delay each -- which is the only step
// of any size in this spec. On a quiet box that loop alone runs ~19s, two
// thirds of the config's 30s test timeout, so a test that declares no budget
// spends most of its clock inside one nested bound and reds on any slower
// machine while that bound still has room. Both halves are one defect seen
// from opposite ends, so the scenario clock is the sum of the bounds nested
// inside it plus a margin for the work between them -- the derivation
// terminal-scroll-on-resize and terminal-switch-timing use for their own.
const ENV_INIT_ERROR_BUDGET_MS = 45_000;
const SCENARIO_MARGIN_MS = 30_000;
const SCENARIO_BUDGET_MS = ENV_INIT_ERROR_BUDGET_MS + SCENARIO_MARGIN_MS;

// How long each state reload is held in the case below, and how many reloads
// the handler makes before it gives up -- the latter must match
// ENVIRONMENT_INIT_RELOAD_ATTEMPTS (frontend/src/app/wailsEventThunks.ts), or
// the derivation below stops covering the case it exists for.
//
// The hold's clock is the 30s test budget this spec used to inherit: eight
// attempts held this long put the loop past 30s with room to spare, so the
// case reds for the right reason when that budget comes back, and a shorter
// hold stops reliably disagreeing with the clock it exists to disagree with.
const LOAD_STATE_HOLD_MS = 2_000;
const RELOAD_ATTEMPTS = 8;

// The slow-reload case converges on the same error one hold later per attempt,
// so its own bound is the error budget above plus the holds it adds, and its
// scenario clock is that plus the same margin. Deriving it that way is what
// keeps it exactly as contention-tolerant as the case above it: reusing the
// fixed 45s would have it red at half the load the other one survives, which
// is the class this file is being converged out of rather than into.
const SLOW_RELOAD_BUDGET_MS = ENV_INIT_ERROR_BUDGET_MS + RELOAD_ATTEMPTS * LOAD_STATE_HOLD_MS;
const SLOW_SCENARIO_BUDGET_MS = SLOW_RELOAD_BUDGET_MS + SCENARIO_MARGIN_MS;

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
    test.setTimeout(SCENARIO_BUDGET_MS);
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
    // (8 attempts, a state reload plus a 400ms delay each) before raising the
    // error, and the wait here must clear that loop's own worst case with real
    // margin rather than the fast-path duration a lightly-loaded machine sees.
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
    // The case above only reds on a machine slow enough to lose the race to a
    // 30s budget; this one forces the same shape on demand by holding the
    // state reloads the handler's loop awaits, so the loop outlasts the 30s
    // this spec used to inherit and still lands inside the scenario clock that
    // replaced it. Nothing is written for the tenant, so the loop runs its
    // whole budget exactly as the case above it does.
    const tenant = uniqueEnvironmentName('slow-ghost-tenant');
    const environment = 'local';

    await page.route('**/__erun_invoke', async (route) => {
      if (parseInvoke(route.request())?.method === 'LoadState') {
        // Deliberate stimulus, not a wait for the app: this hold *is* the
        // contention the case exists to reproduce.
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
});
