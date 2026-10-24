import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// A busy latch with no command end must be released by the env's own idle report.

// The first sweep of a new env always emits once; trigger it up front so it can't overwrite staged
// state later.

test.describe('an idle environment clears a stale desktop latch', () => {
  test('an orphaned running entry stops spinning once the environment reports idle', async ({
    app: _app,
    page,
    seededEnv,
  }) => {
    // The restored env auto-opens, which would hold its row busy for an unrelated reason.
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();
    const spinner = sidebar.getByRole('status', {
      name: `Building ${tenant} / ${environment}`,
    });

    await triggerEnvironmentActivitySweepNow(page);

    await emitRunningBuild(page, tenant, environment);
    await expect(spinner).toBeVisible();

    // A wedged edge reporting no work must not count as idle.
    await emitEnvActivity(page, {
      tenant,
      environment,
      reachable: true,
      observed: false,
      busy: false,
    });
    await expect(spinner).toBeVisible();

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

    // The busy row here is entirely the env's own report, which must not be suppressed.
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

interface EnvironmentActivitySweepBridge {
  go: {
    main: {
      App: {
        TriggerEnvironmentActivitySweep: () => Promise<void>;
      };
    };
  };
}

async function triggerEnvironmentActivitySweepNow(page: Page): Promise<void> {
  await page.evaluate(async () => {
    const bridge = window as unknown as EnvironmentActivitySweepBridge;
    await bridge.go.main.App.TriggerEnvironmentActivitySweep();
  });
}
