import type { Locator, Page } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../fixtures/erunApp.js';
import {
  SEED_ENV_ALPHA,
  SEED_ORCHESTRATOR,
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
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

// Radix's PopoverContent (erun-kit/components/ui/popover.tsx) runs a ~150ms
// zoom-in-95 + slide-in entrance transform on every open. `toBeVisible()`
// resolves the instant the element is visible, not once that transform
// settles, so a `getBoundingClientRect()` read taken right after can land
// mid-transition and report a smaller-than-rest size -- indistinguishable
// from a real difference between what two cards render unless the animation
// is accounted for. Disabling the transform outright (rather than waiting
// past it) makes the settled geometry available from the very first frame;
// the same test-only-workaround shape `Sidebar.ts` already uses to freeze
// `animate-spin` before a hover-stability check.
async function disablePopoverEntranceAnimation(page: Page): Promise<void> {
  await page.addStyleTag({
    content:
      '[role="dialog"][data-state] { animation: none !important; transform: none !important; }',
  });
}

// measureLabelColumnWidth reads a label `dt`'s own rendered width -- which,
// since HOVER_CARD_GRID_CLASS's grid items stretch to fill their column by
// default, equals the resolved width of the fixed label column itself -- and
// pairs it with a same-font, same-document `10ch` probe rather than a
// hard-coded pixel constant, since `ch` resolves against the element's own
// font metrics. Unlike a rendered-size read, this is unaffected by the
// popover's entrance transform (`transform` never changes a grid track's
// resolved size), but callers still disable it for consistency with the
// other bounding-rect reads in this file.
async function measureLabelColumnWidth(label: Locator): Promise<{ actual: number; tenCh: number }> {
  return label.evaluate((el) => {
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
  });
}

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
    page,
  }) => {
    await disablePopoverEntranceAnimation(page);
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
    page,
  }) => {
    await disablePopoverEntranceAnimation(page);
    // Both sides are freshly-seeded, uniquely-named envs rather than the
    // shared SEED_ENV_ALPHA baseline row. SEED_ENV_ALPHA's zone 2 (Activity,
    // Usage, Cloud node) is not reset between specs -- another spec earlier
    // in the same worker (e.g. sidebar-environment-usage.spec.ts) can leave a
    // real Usage reading cached on it, stacking an age caption under the
    // headline that a pristine env's zone 2 never renders. Comparing against
    // that shared, mutable row made this assertion depend on suite ordering
    // instead of on the fixed layout it's meant to lock down.
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
      const plainCard = app.sidebar.envHoverCard(SEED_TENANT, plainEnvironment);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, plainEnvironment);
      await expect(plainCard).toBeVisible();
      const plainZone2Height = await plainCard
        .locator('dl')
        .nth(1)
        .evaluate((el) => el.getBoundingClientRect().height);

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
      removeEnvironment(SEED_TENANT, plainEnvironment);
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

// #1958 narrowed HOVER_CARD_GRID_CLASS's shared label column from 13ch to
// 10ch. Both cards render through the one shared constant (Sidebar.HoverCardRow.tsx),
// so both are asserted here rather than just the env card.
test.describe('sidebar hover card label column narrowed to 10ch (#1958)', () => {
  test('the env card label column resolves to 10ch, not the old 13ch', async ({ app, page }) => {
    await disablePopoverEntranceAnimation(page);
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(card).toBeVisible();
    const { actual, tenCh } = await measureLabelColumnWidth(card.locator('dt:text-is("Version")'));
    expect(actual).toBeCloseTo(tenCh, 0);
  });

  test('the orchestrator card shares the same 10ch label column', async ({ app, page }) => {
    await disablePopoverEntranceAnimation(page);
    const card = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);
    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    await expect(card).toBeVisible();
    const { actual, tenCh } = await measureLabelColumnWidth(card.locator('dt:text-is("Status")'));
    expect(actual).toBeCloseTo(tenCh, 0);
  });
});
