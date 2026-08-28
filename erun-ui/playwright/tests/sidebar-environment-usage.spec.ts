import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../fixtures/seedRoot.js';

// No surface that lists environments reported their usage, so comparing two
// environments meant opening the Manage dialog once per environment. This
// spec locks in the fix: the environment-usage sweep's cached reading
// (environment_usage.go) renders on the environment hover card and the
// orchestrator hover card's per-environment lines, both driven off the one
// `env-usage` Wails event, with its own age and a visible staleness flag —
// and confirms hovering never triggers a probe of its own.
//
// Against origin/main every assertion below that looks for a usage row or
// figure fails outright: neither hover card renders anything from a usage
// reading at all.

interface UsageEvent {
  tenant: string;
  environment: string;
  usage: {
    tenant: string;
    environment: string;
    available: boolean;
    message?: string;
    cpu: { available: boolean; utilization?: string; quota?: string };
    memory: {
      available: boolean;
      unlimited?: boolean;
      current?: string;
      limit?: string;
      percentOfLimit?: number;
      oomKills: number;
    };
  };
  observedAtUnix: number;
  staleAfterSeconds: number;
}

function freshUsagePayload(overrides: Partial<UsageEvent> = {}): UsageEvent {
  return {
    tenant: SEED_TENANT,
    environment: SEED_ENV_ALPHA,
    usage: {
      tenant: SEED_TENANT,
      environment: SEED_ENV_ALPHA,
      available: true,
      cpu: { available: true, utilization: '12.0%', quota: '2.00 cores' },
      memory: {
        available: true,
        current: '512Mi',
        limit: '2048Mi',
        percentOfLimit: 25,
        oomKills: 0,
      },
    },
    observedAtUnix: Math.floor(Date.now() / 1000),
    staleAfterSeconds: 90,
    ...overrides,
  };
}

async function emitEnvUsage(page: Page, payload: UsageEvent): Promise<void> {
  await page.evaluate((event) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('env-usage', event);
  }, payload);
}

// The backend's own usage sweep also runs on a timer against the seeded
// (never-deployed, unreachable) alpha env and can overwrite the injected
// reading with its own "not running" observation, so every assertion driven
// by this helper re-drives the event until it converges, bounded by a real
// timeout rather than a guessed delay — mirrors driveEnvActivity in
// sidebar-orchestrator-hover-card-activity.spec.ts.
async function driveEnvUsage(
  page: Page,
  event: UsageEvent,
  assertions: () => Promise<void>,
): Promise<void> {
  await expect(async () => {
    await emitEnvUsage(page, event);
    await assertions();
  }).toPass({ timeout: 20_000 });
}

test.describe('environment usage on the hover cards', () => {
  test('the environment hover card renders a fresh reading with its age', async ({ app, page }) => {
    await app.reboot();

    const dialog = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await driveEnvUsage(page, freshUsagePayload(), async () => {
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(dialog).toBeVisible({ timeout: 1_000 });
      await expect(dialog).toContainText('CPU 12.0%', { timeout: 1_000 });
      await expect(dialog).toContainText('Mem 25% of 2048Mi', { timeout: 1_000 });
      // The figure carries a unit, a window (the % is "of" the limit), and its
      // own age -- not a bare number an operator cannot interpret.
      await expect(dialog).toContainText('As of', { timeout: 1_000 });
      await expect(dialog).not.toContainText('Stale', { timeout: 1_000 });
    });

    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/environment-usage-visual/env-hover-card-fresh.png',
    });
  });

  test('a reading older than the sweep interval is marked stale, not shown as live', async ({
    app,
    page,
  }) => {
    await app.reboot();

    const dialog = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await driveEnvUsage(
      page,
      freshUsagePayload({
        observedAtUnix: Math.floor(Date.now() / 1000) - 300,
        staleAfterSeconds: 90,
      }),
      async () => {
        await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
        await expect(dialog).toBeVisible({ timeout: 1_000 });
        await expect(dialog).toContainText('Stale', { timeout: 1_000 });
      },
    );

    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/environment-usage-visual/env-hover-card-stale.png',
    });
  });

  test('an environment with no pod to measure states so, never a bare 0%', async ({
    app,
    page,
  }) => {
    await app.reboot();

    const dialog = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await driveEnvUsage(
      page,
      freshUsagePayload({
        usage: {
          tenant: SEED_TENANT,
          environment: SEED_ENV_ALPHA,
          available: false,
          message: 'Not running, or not open here: there is no runtime pod to measure.',
          cpu: { available: false },
          memory: { available: false, oomKills: 0 },
        },
      }),
      async () => {
        await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
        await expect(dialog).toBeVisible({ timeout: 1_000 });
        await expect(dialog).toContainText('there is no runtime pod to measure', {
          timeout: 1_000,
        });
        await expect(dialog).not.toContainText('0%', { timeout: 1_000 });
      },
    );

    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/environment-usage-visual/env-hover-card-no-pod.png',
    });
  });

  // pw-orch (the seeded orchestrator) links pw/alpha, so driving one env-usage
  // event and observing both cards is the actual regression check for "one
  // event, multiple consumers": the two surfaces must never disagree about
  // what this environment is using.
  test('the orchestrator card renders the same joined reading for its linked environment', async ({
    app,
    page,
  }) => {
    await app.reboot();

    const dialog = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);
    await driveEnvUsage(page, freshUsagePayload(), async () => {
      await page.mouse.move(0, 0);
      await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();
      await expect(dialog).toBeVisible({ timeout: 1_000 });
      await expect(dialog).toContainText(`${SEED_TENANT} / ${SEED_ENV_ALPHA}`, { timeout: 1_000 });
      await expect(dialog).toContainText('CPU 12.0%', { timeout: 1_000 });
      await expect(dialog).toContainText('Mem 25% of 2048Mi', { timeout: 1_000 });
    });

    await dialog.screenshot({
      path: '/home/erun/.erun/outputs/environment-usage-visual/orchestrator-card-usage.png',
    });
  });

  // The trap this whole feature exists to avoid: a hover-triggered probe. The
  // usage figure must come only from the cached sweep reading, never from a
  // fresh kubectl-exec fired by the hover gesture itself.
  test('hovering an environment triggers no LoadRuntimeUsage call', async ({ app, page }) => {
    let calls = 0;
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (body.method === 'LoadRuntimeUsage') {
        calls += 1;
      }
      await route.continue();
    });
    await app.reboot();

    for (let i = 0; i < 5; i += 1) {
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA)).toBeVisible();
      await page.mouse.move(0, 0);
    }

    expect(calls).toBe(0);
  });
});
