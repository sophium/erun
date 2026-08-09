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
      await expect(spinner).toHaveCount(0, { timeout: 1_000 });
    }).toPass({ timeout: 20_000 });
  });

  test('an environment that reports work still spins, whoever started it', async ({
    app: _app,
    page,
    seededEnv,
  }) => {
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
      ).toBeVisible({ timeout: 1_000 });
    }).toPass({ timeout: 20_000 });
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
