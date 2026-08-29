import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';

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

// The backend sweeps every environment on its own timer (environmentActivityInterval
// in environment_activity.go) and reports these inert ones unreachable, so an emit
// this spec makes can be overwritten before the row settles. Re-driving until it
// converges is the right shape; the two budgets below are what make it converge.
// The settle wait has to cover a loaded emit → event → re-render round-trip: at 1s
// a loaded machine missed it, and because every retry re-emits the latch first, no
// later attempt inherited the previous one's progress. The outer budget is sized to
// the sweep, not guessed: at exactly one period it passes or fails on where the tick
// happens to land — observed doing both, converging at 20.2s once and exhausting the
// budget once. Two full periods plus margin means a sweep landing anywhere inside
// the window still leaves a whole period for the emits to converge.
const SWEEP_PERIOD_MS = 20_000;
const SETTLE_TIMEOUT_MS = 5_000;
// Four periods, not two. The sweep is what releases the latch, so convergence
// needs a tick to land *and* the emit that follows it to settle inside the same
// window. On an idle machine two periods was ample; once the suite runs specs
// in parallel the settle is slow enough that two windows no longer reliably
// contain one, and the budget expired rather than the logic failing. Widening
// the window is the honest fix here -- the sweep cannot be shortened (it would
// overwrite the staged state more often) nor lengthened (it would never release
// the latch at all), so the only free variable is how long we let it converge.
const CONVERGE_TIMEOUT_MS = SWEEP_PERIOD_MS * 4 + 20_000;

test.describe('an idle environment clears a stale desktop latch', () => {
  test('an orphaned running entry stops spinning once the environment reports idle', async ({
    app: _app,
    page,
    seededEnv,
  }) => {
    // Convergence is paced by the backend's own sweep, so this test outlasts the
    // default budget by design rather than by accident.
    // test.slow() triples the default budget, which is not enough to contain
    // CONVERGE_TIMEOUT_MS above; an expect budget can never exceed the test
    // budget holding it, so this is set explicitly rather than multiplied.
    test.setTimeout(CONVERGE_TIMEOUT_MS + 60_000);
    // A per-test environment, not the restored one: the harness auto-opens that
    // one, and an open in flight is this desktop's own operation, which stays
    // authoritative by design and would hold the row busy for an unrelated
    // reason.
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();
    const spinner = sidebar.getByRole('status', {
      name: `Building ${tenant} / ${environment}`,
    });

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

    // The environment's own answer. The backend's sweep also runs against these
    // inert envs and reports them unreachable, so re-drive until it converges
    // rather than racing a single emit — a row that never clears still fails.
    await expect(async () => {
      await emitRunningBuild(page, tenant, environment);
      await emitEnvActivity(page, {
        tenant,
        environment,
        reachable: true,
        observed: true,
        busy: false,
      });
      await expect(spinner).toHaveCount(0, { timeout: SETTLE_TIMEOUT_MS });
    }).toPass({ timeout: CONVERGE_TIMEOUT_MS });
  });

  test('an environment that reports work still spins, whoever started it', async ({
    app: _app,
    page,
    seededEnv,
  }) => {
    test.slow();
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();

    // Nothing was started from this desktop at all: the busy row here is
    // entirely the environment's own report, which is the case the corrective
    // must not be able to suppress.
    await expect(async () => {
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
      ).toBeVisible({ timeout: SETTLE_TIMEOUT_MS });
    }).toPass({ timeout: CONVERGE_TIMEOUT_MS });
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
