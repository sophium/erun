import type { Page } from '@playwright/test';

import { artifactPath } from '../../../fixtures/artifacts.js';
import { captureHoverCard, expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The env-usage sweep's cached reading renders on the env and orchestrator hover cards, with its
// age and staleness; hovering never probes.

interface UsageEvent {
  tenant: string;
  environment: string;
  usage: {
    tenant: string;
    environment: string;
    available: boolean;
    message?: string;
    cpu: { available: boolean; utilization?: string; utilizationPercent?: number; quota?: string };
    memory: {
      available: boolean;
      unlimited?: boolean;
      current?: string;
      limit?: string;
      percentOfLimit?: number;
      oomKills: number;
    };
    requests?: {
      runtime: { cpuMilli?: number; memoryBytes?: number; cpu?: string; memory?: string };
      unavailable?: string;
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
      cpu: { available: true, utilization: '12.0%', utilizationPercent: 12, quota: '2.00 cores' },
      memory: {
        available: true,
        current: '512Mi',
        limit: '2048Mi',
        percentOfLimit: 25,
        oomKills: 0,
      },
      // The pod spec's reservation; the 2048Mi above is only the ceiling.
      requests: {
        runtime: { cpuMilli: 250, memoryBytes: 1073741824, cpu: '0.25 CPU', memory: '1024Mi' },
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

// The backend sweep can overwrite the injected reading, so re-drive the event until assertions
// converge.
async function driveEnvUsage(
  page: Page,
  event: UsageEvent,
  assertions: () => Promise<void>,
): Promise<void> {
  await expect(async () => {
    await emitEnvUsage(page, event);
    await assertions();
  }).toPass();
}

test.describe('environment usage on the hover cards', () => {
  test('the environment hover card renders a fresh reading with its age', async ({ app, page }) => {
    // The nested hover retry can consume the 30s default; driveEnvUsage has no cap of its own so
    // this budget applies.
    test.setTimeout(60_000);
    await app.reboot();

    const dialog = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await driveEnvUsage(page, freshUsagePayload(), async () => {
      // Move off first: re-hovering a row the pointer never left fires no mouseenter.
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(dialog).toBeVisible({ timeout: 1_000 });
      await expect(dialog.getByText('CPU', { exact: true })).toBeVisible({ timeout: 1_000 });
      await expect(dialog.getByText('Memory', { exact: true })).toBeVisible({ timeout: 1_000 });
      await expect(dialog).toContainText('12.0%', { timeout: 1_000 });
      // `of 2048Mi` alone would read as a 2GiB reservation.
      await expect(dialog).toContainText('of 2048Mi limit', { timeout: 1_000 });
      await expect(dialog).toContainText('1024Mi requested', { timeout: 1_000 });
      await expect(dialog).toContainText('0.25 CPU requested', { timeout: 1_000 });
      // 12% -> 2 segments, 25% -> 3; neither is amber.
      const strips = dialog.locator('[data-decile-fill]');
      await expect(strips).toHaveCount(2, { timeout: 1_000 });
      await expect(strips.nth(0)).toHaveAttribute('data-decile-fill', '2', { timeout: 1_000 });
      await expect(strips.nth(1)).toHaveAttribute('data-decile-fill', '3', { timeout: 1_000 });
      await expect(strips.nth(0)).toHaveAttribute('data-decile-alert', 'false', {
        timeout: 1_000,
      });
      await expect(dialog).toContainText('As of', { timeout: 1_000 });
      await expect(dialog).not.toContainText('Stale', { timeout: 1_000 });
      // Capture while converged; outside the callback the sweep can overwrite the reading.
      await captureHoverCard(
        dialog,
        artifactPath('test-results/environment-usage-visual/env-hover-card-fresh.png'),
      );
    });
  });

  test('a reading older than the sweep interval is marked stale, not shown as live', async ({
    app,
    page,
  }) => {
    // See the preceding test.
    test.setTimeout(60_000);
    await app.reboot();

    const dialog = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await driveEnvUsage(
      page,
      freshUsagePayload({
        observedAtUnix: Math.floor(Date.now() / 1000) - 300,
        staleAfterSeconds: 90,
      }),
      async () => {
        // Move off first: re-hovering a row the pointer never left fires no mouseenter.
        await page.mouse.move(0, 0);
        await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
        await expect(dialog).toBeVisible({ timeout: 1_000 });
        await expect(dialog).toContainText('Stale', { timeout: 1_000 });
        // Capture while converged; outside the callback the sweep can overwrite the reading.
        await captureHoverCard(
          dialog,
          artifactPath('test-results/environment-usage-visual/env-hover-card-stale.png'),
        );
      },
    );
  });

  // A measured zero renders an empty strip; a never-sampled env renders none.
  test('a measured zero renders an empty strip, not a missing one', async ({ app, page }) => {
    test.setTimeout(60_000);
    await app.reboot();

    const dialog = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await driveEnvUsage(
      page,
      freshUsagePayload({
        usage: {
          tenant: SEED_TENANT,
          environment: SEED_ENV_ALPHA,
          available: true,
          // The Go side marks these `omitempty`, so a 0% arrives with only the formatted string.
          cpu: { available: true, utilization: '0.0%' },
          memory: { available: true, current: '0Mi', limit: '2048Mi', oomKills: 0 },
        },
      }),
      async () => {
        await page.mouse.move(0, 0);
        await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
        await expect(dialog).toBeVisible({ timeout: 1_000 });

        const strips = dialog.locator('[data-decile-fill]');
        await expect(strips).toHaveCount(2, { timeout: 1_000 });
        await expect(strips.nth(0)).toHaveAttribute('data-decile-fill', '0', { timeout: 1_000 });
        await expect(strips.nth(1)).toHaveAttribute('data-decile-fill', '0', { timeout: 1_000 });
        await expect(dialog).toContainText('0.0%', { timeout: 1_000 });
      },
    );
  });

  // The segment count carries the same claim as the colour.
  test('a reading above 80% renders amber and nine filled segments', async ({ app, page }) => {
    test.setTimeout(60_000);
    await app.reboot();

    const dialog = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await driveEnvUsage(
      page,
      freshUsagePayload({
        usage: {
          tenant: SEED_TENANT,
          environment: SEED_ENV_ALPHA,
          available: true,
          cpu: {
            available: true,
            utilization: '85.0%',
            utilizationPercent: 85,
            quota: '2.00 cores',
          },
          memory: {
            available: true,
            current: '900Mi',
            limit: '1024Mi',
            percentOfLimit: 88,
            oomKills: 0,
          },
        },
      }),
      async () => {
        await page.mouse.move(0, 0);
        await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
        await expect(dialog).toBeVisible({ timeout: 1_000 });

        const strips = dialog.locator('[data-decile-fill]');
        // ceil(85/10) = 9, ceil(88/10) = 9.
        await expect(strips.nth(0)).toHaveAttribute('data-decile-fill', '9', { timeout: 1_000 });
        await expect(strips.nth(1)).toHaveAttribute('data-decile-fill', '9', { timeout: 1_000 });
        await expect(strips.nth(0)).toHaveAttribute('data-decile-alert', 'true', {
          timeout: 1_000,
        });
        await expect(strips.nth(1)).toHaveAttribute('data-decile-alert', 'true', {
          timeout: 1_000,
        });
      },
    );
  });

  test('an environment with no pod to measure states so, never a bare 0%', async ({
    app,
    page,
  }) => {
    // See the first driveEnvUsage test.
    test.setTimeout(60_000);
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
        // Move off first: re-hovering a row the pointer never left fires no mouseenter.
        await page.mouse.move(0, 0);
        await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
        await expect(dialog).toBeVisible({ timeout: 1_000 });
        await expect(dialog).toContainText('there is no runtime pod to measure', {
          timeout: 1_000,
        });
        await expect(dialog).not.toContainText('0%', { timeout: 1_000 });
        // An empty strip would read as "measured 0%".
        await expect(dialog.locator('[data-decile-fill]')).toHaveCount(0, { timeout: 1_000 });
        // Capture while converged; outside the callback the sweep can overwrite the reading.
        await captureHoverCard(
          dialog,
          artifactPath('test-results/environment-usage-visual/env-hover-card-no-pod.png'),
        );
      },
    );
  });

  // pw-orch links pw/alpha, so one env-usage event must render identically on both cards.
  test('the orchestrator card renders the same joined reading for its linked environment', async ({
    app,
    page,
  }) => {
    // See the first driveEnvUsage test.
    test.setTimeout(60_000);
    await app.reboot();

    const dialog = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);
    await driveEnvUsage(page, freshUsagePayload(), async () => {
      await page.mouse.move(0, 0);
      await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
      await expect(dialog).toBeVisible({ timeout: 1_000 });
      await expect(dialog).toContainText(`${SEED_TENANT} / ${SEED_ENV_ALPHA}`, { timeout: 1_000 });
      await expect(dialog).toContainText('CPU 12.0%', { timeout: 1_000 });
      await expect(dialog).toContainText('Mem 25% of 2048Mi', { timeout: 1_000 });
      // Capture while converged; outside the callback the sweep can overwrite the reading.
      await captureHoverCard(
        dialog,
        artifactPath('test-results/environment-usage-visual/orchestrator-card-usage.png'),
      );
    });
  });

  // Usage must come only from the cached sweep, never a hover-triggered probe.
  test('hovering an environment triggers no LoadRuntimeUsage call', async ({ app, page }) => {
    // Five hover round-trips need more than the 30s default under contention.
    test.setTimeout(60_000);
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
      await app.sidebar.readEnvHoverCard(SEED_TENANT, SEED_ENV_ALPHA, async (card) => {
        await expect(card).toBeVisible({ timeout: 1_000 });
      });
      await page.mouse.move(0, 0);
    }

    // Wait for the card to close before reading the counter, so a late call can still be caught.
    await app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA).waitFor({ state: 'hidden' });
    expect(calls).toBe(0);
  });

  // The reservation is read separately and can fail; a failed read must not render as zero or hide
  // the rest.
  test('an unread reservation states no request rather than a zero one', async ({ app, page }) => {
    test.setTimeout(60_000);
    await app.reboot();

    const dialog = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await driveEnvUsage(
      page,
      freshUsagePayload({
        usage: {
          tenant: SEED_TENANT,
          environment: SEED_ENV_ALPHA,
          available: true,
          cpu: {
            available: true,
            utilization: '12.0%',
            utilizationPercent: 12,
            quota: '2.00 cores',
          },
          memory: {
            available: true,
            current: '512Mi',
            limit: '2048Mi',
            percentOfLimit: 25,
            oomKills: 0,
          },
          requests: { runtime: {}, unavailable: 'kubectl get pods: connection refused' },
        },
      }),
      async () => {
        await page.mouse.move(0, 0);
        await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
        await expect(dialog).toBeVisible({ timeout: 1_000 });
        await expect(dialog).toContainText('of 2048Mi limit', { timeout: 1_000 });
        await expect(dialog).toContainText('12.0%', { timeout: 1_000 });
        await expect(dialog).not.toContainText('requested', { timeout: 1_000 });
      },
    );
  });
});
