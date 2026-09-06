import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// A row span for over six hours with nothing running (#955). Every input to its
// busy state was desktop-local — set when the desktop starts something, cleared
// when the desktop sees it end — so any path that loses the ending left a latch
// with nothing able to release it. The environment's own report was already
// fetched by the row and then discarded.
//
// This is that failure staged whole: a running command entry with no ending ever
// delivered, which is precisely what a command driven from a terminal or over
// MCP looks like from here, and then the environment answering that it is idle.
// The unit derivation is owned by Sidebar.helpers.test.ts and the observation by
// environment_activity_observed_test.go; what only the running app can show is
// that the spinner an orphaned latch produces actually goes away.

// The backend's own sweep (environmentActivityInterval in
// environment_activity.go) is not noise this spec has to survive -- it is the
// mechanism that releases the latch, so it must keep running. What has to be
// removed is the race: emitEnvActivityIfChanged only re-emits an environment's
// activity on a transition, so the very first observation of a brand-new
// environment always emits once, real or synthetic, and that one-time emit
// could otherwise land at an arbitrary point later in the test and overwrite
// state this spec has staged. TriggerEnvironmentActivitySweepNow drives that
// one-time emit deterministically, before anything is staged, so every later
// automatic tick observes the same (unreachable) environment, finds nothing
// changed, and stays quiet for the rest of the test -- no timer to wait for,
// no budget to size.

test.describe('an idle environment clears a stale desktop latch', () => {
  test('an orphaned running entry stops spinning once the environment reports idle', async ({
    app: _app,
    page,
    seededEnv,
  }) => {
    // A per-test environment, not the restored one: the harness auto-opens that
    // one, and an open in flight is this desktop's own operation, which stays
    // authoritative by design and would hold the row busy for an unrelated
    // reason.
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();
    const spinner = sidebar.getByRole('status', {
      name: `Building ${tenant} / ${environment}`,
    });

    await triggerEnvironmentActivitySweepNow(page);

    // The latch, and no ending for it — the desktop is left believing a build
    // it started is still running.
    await emitRunningBuild(page, tenant, environment);
    await expect(spinner).toBeVisible();

    // An edge that has wedged behind a port that still answers reports no work
    // because nobody asked it. That must not pass for the environment saying it
    // is idle, or a row would stop spinning while the work is still running.
    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: true,
      observed: false,
      busy: false,
    });
    await expect(spinner).toBeVisible();

    // The environment's own answer clears the latch.
    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: true,
      observed: true,
      busy: false,
    });
    await expect(spinner).toHaveCount(0);
  });

  test('an environment that reports work still spins, whoever started it', async ({
    app: _app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();

    await triggerEnvironmentActivitySweepNow(page);

    // Nothing was started from this desktop at all: the busy row here is
    // entirely the environment's own report, which is the case the corrective
    // must not be able to suppress.
    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: true,
      observed: true,
      busy: true,
      detail: 'holding: gradle-build',
    });
    await expect(
      sidebar.getByRole('status', {
        name: `${tenant} / ${environment} is busy — holding: gradle-build`,
      }),
    ).toBeVisible();
  });
});

async function emitRunningBuild(page: Page, tenant: string, environment: string): Promise<void> {
  await page.evaluate(
    ({ tenant, environment }) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
        }
      ).runtime;
      const now = new Date().toISOString();
      runtime.EventsEmit('activity:state', {
        id: 'stale-latch-spec-build',
        command: 'build',
        tenant,
        environment,
        status: 'running',
        startedAt: now,
        lastUpdated: now,
        source: 'trace',
        summary: `build ${tenant}/${environment}`,
      });
    },
    { tenant, environment },
  );
}

// Mirrors the env-activity event erun-ui/environment_activity.go emits per tick.
async function emitEnvActivity(
  page: Page,
  payload: {
    tenant: string;
    environment: string;
    reachable: boolean;
    observed: boolean;
    busy: boolean;
    detail?: string;
  },
): Promise<void> {
  await page.evaluate((event) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('env-activity', event);
  }, payload);
}

// The desktop backend as the browser sees it, narrowed to the one call here.
interface EnvironmentActivitySweepBridge {
  go: {
    main: {
      App: {
        TriggerEnvironmentActivitySweep: () => Promise<void>;
      };
    };
  };
}

// Drives environment_activity.go's sweep synchronously (see the App method's
// own doc comment for why this consumes the sweep's one-time emit up front
// rather than parking or widening anything).
async function triggerEnvironmentActivitySweepNow(page: Page): Promise<void> {
  await page.evaluate(async () => {
    const bridge = window as unknown as EnvironmentActivitySweepBridge;
    await bridge.go.main.App.TriggerEnvironmentActivitySweep();
  });
}
