import type { Locator, Page } from '@playwright/test';

import {
  disablePopoverEntranceAnimation,
  expect,
  test,
  waitForSeededRow,
} from '../../../fixtures/erunApp.js';
import {
  SEED_ENV_ALPHA,
  SEED_ORCHESTRATOR,
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  seedEnvironmentWithRuntimeVersions,
  seedRuntimeEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// Locks the env hover card layout: a fixed label column shared by every row, and two zones split by
// a hairline.

// The dt stretches to fill the label column; compared against a same-font `10ch` probe.
async function measureLabelColumnWidth(label: Locator): Promise<{ actual: number; tenCh: number }> {
  return label.evaluate(
    (el) => {
      const font = window.getComputedStyle(el).font;
      const probe = document.createElement('span');
      probe.style.position = 'fixed';
      probe.style.visibility = 'hidden';
      probe.style.whiteSpace = 'nowrap';
      probe.style.font = font;
      probe.style.width = '10ch';
      document.body.appendChild(probe);
      const tenCh = probe.getBoundingClientRect().width;
      probe.remove();
      return { actual: el.getBoundingClientRect().width, tenCh };
    },
    undefined,
    { timeout: 1_000 },
  );
}

async function emitEnvUsage(
  page: Page,
  tenant: string,
  environment: string,
  ageSeconds: number,
  staleAfterSeconds: number,
  dind?: {
    cpu: {
      available: boolean;
      unavailable?: string;
      utilization?: string;
      quota?: string;
      usageUsec?: number;
    };
    memory: { available: boolean; current?: string; limit?: string; unlimited?: boolean };
  },
  // An absent `dind` means either no sidecar or a failed exec into one; only the latter sets
  // excludesBuilds.
  carriesSidecar = dind !== undefined,
): Promise<void> {
  await page.evaluate(
    ({
      tenant,
      environment,
      ageSeconds,
      staleAfterSeconds,
      dind: sidecar,
      carriesSidecar: hasSidecar,
    }) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('env-usage', {
        tenant,
        environment,
        usage: {
          tenant,
          environment,
          available: true,
          cpu: { available: true, utilization: '12.0%', quota: '2.00 cores' },
          memory: {
            available: true,
            current: '512Mi',
            limit: '2048Mi',
            percentOfLimit: 25,
            oomKills: 0,
          },
          // A sidecar with no reading is what a failed exec produces.
          ...(hasSidecar
            ? { excludesBuilds: true, ...(sidecar === undefined ? {} : { dind: sidecar }) }
            : {}),
        },
        observedAtUnix: Math.floor(Date.now() / 1000) - ageSeconds,
        staleAfterSeconds,
      });
    },
    { tenant, environment, ageSeconds, staleAfterSeconds, dind, carriesSidecar },
  );
}

async function emitStaleEnvUsage(page: Page, tenant: string, environment: string): Promise<void> {
  await emitEnvUsage(page, tenant, environment, 600, 90);
}

async function emitFreshEnvUsage(page: Page, tenant: string, environment: string): Promise<void> {
  await emitEnvUsage(page, tenant, environment, 5, 90);
}

test.describe('sidebar env hover card layout (#1901)', () => {
  test('the label column is the same width whether or not the conditional Line mismatch row is present', async ({
    app,
    page,
  }) => {
    await disablePopoverEntranceAnimation(page);
    // A re-render can drop the card mid-read, so hover and reads retry together.
    let plainLabelWidth = 0;
    await app.sidebar.readEnvHoverCard(SEED_TENANT, SEED_ENV_ALPHA, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });
      plainLabelWidth = await card
        .locator('dt:text-is("Version")')
        .evaluate((el) => el.getBoundingClientRect().width, undefined, { timeout: 1_000 });
    });

    const environment = uniqueEnvironmentName('line-mismatch-width');
    seedEnvironmentWithRuntimeVersions(SEED_TENANT, environment, {
      runtimeVersion: '1.0.86',
      runtimeImage: 'ghcr.io/sophium/erun-devops',
      runtimeRunningImage: 'ghcr.io/sophium/frs-devops:1.0.86',
    });
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      let mismatchLabelWidth = 0;
      await app.sidebar.readEnvHoverCard(SEED_TENANT, environment, async (card) => {
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(card.getByText('Line mismatch', { exact: true })).toBeVisible({
          timeout: 1_000,
        });
        mismatchLabelWidth = await card
          .locator('dt:text-is("Version")')
          .evaluate((el) => el.getBoundingClientRect().width, undefined, { timeout: 1_000 });
      });

      expect(mismatchLabelWidth).toBeCloseTo(plainLabelWidth, 0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('the card renders two zones, separated by a visible boundary', async ({ app }) => {
    let secondZoneBorder = '';
    let firstZoneBorder = '';
    await app.sidebar.readEnvHoverCard(SEED_TENANT, SEED_ENV_ALPHA, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });

      // The zones are two `dl`s, so the count is part of the contract.
      const zones = card.locator('dl');
      await expect(zones).toHaveCount(2, { timeout: 1_000 });

      secondZoneBorder = await zones
        .nth(1)
        .evaluate((el) => window.getComputedStyle(el).borderTopWidth, undefined, {
          timeout: 1_000,
        });
      firstZoneBorder = await zones
        .nth(0)
        .evaluate((el) => window.getComputedStyle(el).borderTopWidth, undefined, {
          timeout: 1_000,
        });

      // Zone 2's conditional rows depend on live state this test doesn't drive; their shapes are
      // covered elsewhere.
      await expect(zones.nth(0)).toContainText('Version', { timeout: 1_000 });
      await expect(zones.nth(0)).toContainText('Working on', { timeout: 1_000 });
      await expect(zones.nth(1)).toContainText('Activity', { timeout: 1_000 });
      await expect(zones.nth(1)).not.toContainText('Working on', { timeout: 1_000 });
    });

    expect(Number.parseFloat(secondZoneBorder)).toBeGreaterThan(0);
    expect(Number.parseFloat(firstZoneBorder)).toBe(0);
  });

  test('adding the conditional Line mismatch row changes only zone 1, not zone 2', async ({
    app,
    page,
  }) => {
    await disablePopoverEntranceAnimation(page);
    // Fresh envs, since SEED_ENV_ALPHA's zone 2 may carry a reading from another spec.
    // Compare zone 2's rows, not its height: a sweep reading can land between reads and add a
    // caption line.
    const plainEnvironment = uniqueEnvironmentName('line-mismatch-zone-plain');
    seedEnvironment(SEED_TENANT, plainEnvironment);
    const environment = uniqueEnvironmentName('line-mismatch-zone');
    seedEnvironmentWithRuntimeVersions(SEED_TENANT, environment, {
      runtimeVersion: '1.0.86',
      runtimeImage: 'ghcr.io/sophium/erun-devops',
      runtimeRunningImage: 'ghcr.io/sophium/frs-devops:1.0.86',
    });
    try {
      await waitForSeededRow(app, SEED_TENANT, plainEnvironment);
      let plainZone2Rows: string[] = [];
      await app.sidebar.readEnvHoverCard(SEED_TENANT, plainEnvironment, async (card) => {
        await expect(card).toBeVisible({ timeout: 1_000 });
        plainZone2Rows = await card.locator('dl').nth(1).locator('dt').allTextContents();
      });

      await waitForSeededRow(app, SEED_TENANT, environment);
      let mismatchZone2Rows: string[] = [];
      await app.sidebar.readEnvHoverCard(SEED_TENANT, environment, async (card) => {
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(card.getByText('Line mismatch', { exact: true })).toBeVisible({
          timeout: 1_000,
        });
        // Read both zones so a row in the wrong zone can't pass.
        await expect(card.locator('dl').nth(0)).toContainText('Line mismatch', {
          timeout: 1_000,
        });
        mismatchZone2Rows = await card.locator('dl').nth(1).locator('dt').allTextContents();
      });

      expect(mismatchZone2Rows).toEqual(plainZone2Rows);
    } finally {
      removeEnvironment(SEED_TENANT, plainEnvironment);
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // A stale reading must not look more alarming than it is.
  test('a stale usage reading renders degraded, not as an amber warning', async ({ app, page }) => {
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    // A cached reading renders CPU and Memory rows, not a single Usage row.
    const usageValue = card.locator('dt:text-is("CPU") + dd');
    let color = '';
    // The usage sweep can re-render mid-read, so all reads stay in one retry.
    await expect(async () => {
      await emitStaleEnvUsage(page, SEED_TENANT, SEED_ENV_ALPHA);
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('Stale', { timeout: 1_000 });
      await expect(usageValue.locator('svg')).toHaveCount(0, { timeout: 1_000 });
      color = await usageValue
        .locator('span')
        .first()
        .evaluate((el) => window.getComputedStyle(el).color, undefined, { timeout: 1_000 });
    }).toPass();

    // Assert the absence of amber rather than a specific token.
    const components = color.match(/\d+(\.\d+)?/g)?.map(Number) ?? [0, 0, 0];
    const r = components[0] ?? 0;
    const g = components[1] ?? 0;
    const b = components[2] ?? 0;
    expect(r - b, `expected a desaturated colour, got ${color}`).toBeLessThanOrEqual(40);
    expect(g - b, `expected a desaturated colour, got ${color}`).toBeLessThanOrEqual(10);
  });
});

// Both cards share the 10ch label column, so both are asserted.
test.describe('sidebar hover card label column narrowed to 10ch (#1958)', () => {
  test('the env card label column resolves to 10ch, not the old 13ch', async ({ app, page }) => {
    await disablePopoverEntranceAnimation(page);
    let measured = { actual: 0, tenCh: 0 };
    await app.sidebar.readEnvHoverCard(SEED_TENANT, SEED_ENV_ALPHA, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });
      measured = await measureLabelColumnWidth(card.locator('dt:text-is("Version")'));
    });
    expect(measured.actual).toBeCloseTo(measured.tenCh, 0);
  });

  test('the orchestrator card shares the same 10ch label column', async ({ app, page }) => {
    await disablePopoverEntranceAnimation(page);
    let measured = { actual: 0, tenCh: 0 };
    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });
      measured = await measureLabelColumnWidth(card.locator('dt:text-is("Status")'));
    });
    expect(measured.actual).toBeCloseTo(measured.tenCh, 0);
  });
});

// Usage comes from the runtime container's cgroup while builds run in the erun-dind sidecar, so
// build-capable envs always carry the caveat; runtime envs have no sidecar.
test.describe('sidebar env hover card usage caveat for build-capable environments (#1979)', () => {
  test('usage caption for a build-capable environment says it excludes builds', async ({
    app,
    page,
  }) => {
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(async () => {
      await emitFreshEnvUsage(page, SEED_TENANT, SEED_ENV_ALPHA);
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      // The caption names its rows so it can't be read as qualifying the Builds row.
      await expect(card).toContainText('CPU and memory exclude builds', { timeout: 1_000 });
    }).toPass();
  });

  test('usage caption for a runtime environment does not claim it excludes builds', async ({
    app,
    page,
  }) => {
    const environment = uniqueEnvironmentName('usage-caveat-runtime');
    seedRuntimeEnvironment(SEED_TENANT, environment);
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      const card = app.sidebar.envHoverCard(SEED_TENANT, environment);
      await expect(async () => {
        await emitFreshEnvUsage(page, SEED_TENANT, environment);
        await page.mouse.move(0, 0);
        await app.sidebar.hoverEnvironmentRow(SEED_TENANT, environment);
        await expect(card).toBeVisible({ timeout: 1_000 });
        await expect(card).toContainText('As of', { timeout: 1_000 });
      }).toPass();
      await expect(card).not.toContainText('exclude builds');
      await expect(card).not.toContainText('erun-dind sidecar');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The runtime container's CPU can't tell a healthy build from a wedged one, so the sidecar gets
  // its own row.
  test('a build-capable environment renders the sidecar as its own Builds row', async ({
    app,
    page,
  }) => {
    // Nested hover retries need more than the 30s default; the outer retry is uncapped so this
    // budget applies.
    test.setTimeout(60_000);
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(async () => {
      await emitEnvUsage(page, SEED_TENANT, SEED_ENV_ALPHA, 5, 90, {
        cpu: { available: true, utilization: '91.5%', quota: '8.00 cores' },
        memory: { available: true, current: '19.4Gi', limit: '20.0Gi' },
      });
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card.getByText('Builds', { exact: true })).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('91.5%', { timeout: 1_000 });
      await expect(card).toContainText('erun-dind sidecar · 19.4Gi of 20.0Gi', {
        timeout: 1_000,
      });
      await expect(card).toContainText('CPU and memory exclude builds', { timeout: 1_000 });
    }).toPass();
  });

  // Without a cpu.max quota there is no percentage, so show CPU-seconds and no strip.
  test('a sidecar with no CPU quota renders its cumulative CPU-seconds and no strip', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(async () => {
      await emitEnvUsage(page, SEED_TENANT, SEED_ENV_ALPHA, 5, 90, {
        cpu: {
          available: false,
          unavailable:
            'cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against',
          usageUsec: 385919164,
        },
        memory: { available: true, unlimited: true, current: '512Mi' },
      });
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('386 CPU-s', { timeout: 1_000 });
      await expect(card).toContainText('no quota', { timeout: 1_000 });
      await expect(card).toContainText('erun-dind sidecar · 512Mi (no limit)', {
        timeout: 1_000,
      });
      await expect(card.locator('[data-decile-fill]')).toHaveCount(2, { timeout: 1_000 });
    }).toPass();
  });

  // An unread sidecar is not the same as no sidecar, so the row says it couldn't be read.
  test('a build-capable environment whose sidecar could not be read says so instead of showing no Builds row', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(async () => {
      // The sidecar is present but unread, as after a failed exec.
      await emitEnvUsage(page, SEED_TENANT, SEED_ENV_ALPHA, 5, 90, undefined, true);
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card.getByText('Builds', { exact: true })).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('erun-dind sidecar', { timeout: 1_000 });
      await expect(card).toContainText('the sidecar could not be read', { timeout: 1_000 });
      // No strip for an unread value.
      await expect(card.locator('[data-decile-fill]')).toHaveCount(2, { timeout: 1_000 });
    }).toPass();
  });
});
