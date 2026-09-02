import type { Page } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../fixtures/erunApp.js';
import {
  SEED_ENV_ALPHA,
  SEED_TENANT,
  removeEnvironment,
  seedEnvironmentWithRuntimeVersions,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// #1901 unified the env hover card to one row layout with a spacing
// hierarchy (Sidebar.HoverCardRow.tsx): a fixed label column shared by every
// row regardless of which conditional rows are present, and two zones
// (stable identity vs live state) separated by a hairline so a conditional
// row changes only its own zone's height. This spec locks the layout
// contract computationally, mirroring sidebar-hovercard-type-scale.spec.ts's
// approach for the type contract.

async function emitStaleEnvUsage(page: Page, tenant: string, environment: string): Promise<void> {
  await page.evaluate(
    ({ tenant, environment }) => {
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
        },
        observedAtUnix: Math.floor(Date.now() / 1000) - 600,
        staleAfterSeconds: 90,
      });
    },
    { tenant, environment },
  );
}

test.describe('sidebar env hover card layout (#1901)', () => {
  test('the label column is the same width whether or not the conditional Line mismatch row is present', async ({
    app,
  }) => {
    const plainCard = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(plainCard).toBeVisible();
    const plainLabelWidth = await plainCard
      .locator('dt:text-is("Version")')
      .evaluate((el) => el.getBoundingClientRect().width);

    const environment = uniqueEnvironmentName('line-mismatch-width');
    seedEnvironmentWithRuntimeVersions(SEED_TENANT, environment, {
      runtimeVersion: '1.0.86',
      runtimeImage: 'ghcr.io/sophium/erun-devops',
      runtimeRunningImage: 'ghcr.io/sophium/frs-devops:1.0.86',
    });
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      const mismatchCard = app.sidebar.envHoverCard(SEED_TENANT, environment);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, environment);
      await expect(mismatchCard).toBeVisible();
      await expect(mismatchCard.getByText('Line mismatch', { exact: true })).toBeVisible();
      const mismatchLabelWidth = await mismatchCard
        .locator('dt:text-is("Version")')
        .evaluate((el) => el.getBoundingClientRect().width);

      // Same fixed ch-based column (HOVER_CARD_GRID_CLASS) regardless of which
      // conditional rows this particular card happens to render.
      expect(mismatchLabelWidth).toBeCloseTo(plainLabelWidth, 0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('the card renders two zones, separated by a visible boundary', async ({ app }) => {
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(card).toBeVisible();

    // Zone 1 (Version .. Working on) and zone 2 (Activity .. Cloud node) are
    // two separate `dl`s sharing the same grid template -- not one `dl` with
    // a spanning divider row -- so the count itself is part of the contract.
    const zones = card.locator('dl');
    await expect(zones).toHaveCount(2);

    const secondZoneBorder = await zones
      .nth(1)
      .evaluate((el) => window.getComputedStyle(el).borderTopWidth);
    expect(Number.parseFloat(secondZoneBorder)).toBeGreaterThan(0);

    const firstZoneBorder = await zones
      .nth(0)
      .evaluate((el) => window.getComputedStyle(el).borderTopWidth);
    expect(Number.parseFloat(firstZoneBorder)).toBe(0);

    // Live-state rows -- Activity, Usage, Cloud node -- live in the second
    // zone; identity rows -- Version, Working on -- live in the first.
    await expect(zones.nth(0)).toContainText('Version');
    await expect(zones.nth(0)).toContainText('Working on');
    await expect(zones.nth(1)).toContainText('Activity');
    await expect(zones.nth(1)).toContainText('Usage');
    await expect(zones.nth(1)).toContainText('Cloud node');
  });

  test('adding the conditional Line mismatch row changes only zone 1, not zone 2', async ({
    app,
  }) => {
    const plainCard = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(plainCard).toBeVisible();
    const plainZone2Height = await plainCard
      .locator('dl')
      .nth(1)
      .evaluate((el) => el.getBoundingClientRect().height);

    const environment = uniqueEnvironmentName('line-mismatch-zone');
    seedEnvironmentWithRuntimeVersions(SEED_TENANT, environment, {
      runtimeVersion: '1.0.86',
      runtimeImage: 'ghcr.io/sophium/erun-devops',
      runtimeRunningImage: 'ghcr.io/sophium/frs-devops:1.0.86',
    });
    try {
      await waitForSeededRow(app, SEED_TENANT, environment);
      const mismatchCard = app.sidebar.envHoverCard(SEED_TENANT, environment);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, environment);
      await expect(mismatchCard).toBeVisible();
      await expect(mismatchCard.getByText('Line mismatch', { exact: true })).toBeVisible();
      const mismatchZone2Height = await mismatchCard
        .locator('dl')
        .nth(1)
        .evaluate((el) => el.getBoundingClientRect().height);

      expect(mismatchZone2Height).toBeCloseTo(plainZone2Height, 0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // A stale usage reading is degraded (muted, reduced opacity), never amber:
  // nothing the operator did caused it and no action follows from it, so it
  // must not look more alarming than an unmeasured number actually is (also
  // relevant to #1805 -- the percentage itself is not authoritative for a
  // build-capable environment).
  test('a stale usage reading renders degraded, not as an amber warning', async ({ app, page }) => {
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(async () => {
      await emitStaleEnvUsage(page, SEED_TENANT, SEED_ENV_ALPHA);
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('Stale', { timeout: 1_000 });
    }).toPass({ timeout: 20_000 });

    const usageValue = card.locator('dt:text-is("Usage") + dd');
    // No alert icon -- a stale reading is not a warning.
    await expect(usageValue.locator('svg')).toHaveCount(0);
    const color = await usageValue
      .locator('span')
      .first()
      .evaluate((el) => window.getComputedStyle(el).color);
    // amber-700/amber-400 both render with a non-trivial red/green gap from
    // blue; the degraded muted-foreground token is a desaturated grey. Assert
    // the absence of amber rather than a hard-coded token, so a theme edit
    // that keeps the token desaturated does not need to update this test.
    const components = color.match(/\d+(\.\d+)?/g)?.map(Number) ?? [0, 0, 0];
    const r = components[0] ?? 0;
    const g = components[1] ?? 0;
    const b = components[2] ?? 0;
    // Amber requires BOTH gaps at once; asserting each gap alone (rather than
    // combining them into one boolean) keeps this test free of a conditional.
    expect(r - b, `expected a desaturated colour, got ${color}`).toBeLessThanOrEqual(40);
    expect(g - b, `expected a desaturated colour, got ${color}`).toBeLessThanOrEqual(10);
  });
});
