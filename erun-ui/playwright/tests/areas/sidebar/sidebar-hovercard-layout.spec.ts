import type { Locator, Page } from '@playwright/test';

import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
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
  // carriesSidecar distinguishes the two states an absent `dind` covers, and
  // they are not the same reading: an environment that carries no erun-dind
  // sidecar at all (the default, matching the runtime envs the baseline seeds)
  // and an environment that carries one whose exec into it failed. The second
  // is what `excludesBuilds` marks, and it is absent from the payload entirely
  // for the first.
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
          // Absent unless a case asks for it: a runtime-only environment must
          // keep rendering exactly the two rows it always did. A case that
          // carries the sidecar but supplies no reading emits it with no
          // `dind` at all, which is the state the reader produces when its
          // exec into the sidecar fails.
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

// emitFreshEnvUsage reports a reading that has not outlived its own sweep
// interval, so UsageState's caption always renders "As of ... ago" rather
// than the stale branch -- the fresh-branch counterpart of emitStaleEnvUsage,
// used to check the #1979 "excludes builds" caveat on the branch it renders
// most often.
async function emitFreshEnvUsage(page: Page, tenant: string, environment: string): Promise<void> {
  await emitEnvUsage(page, tenant, environment, 5, 90);
}

test.describe('sidebar env hover card layout (#1901)', () => {
  test('the label column is the same width whether or not the conditional Line mismatch row is present', async ({
    app,
    page,
  }) => {
    await disablePopoverEntranceAnimation(page);
    // Hover and every read live inside one retryable block: a re-render
    // (e.g. the boot-time auto-open of SEED_ENV_ALPHA, or the periodic
    // activity/usage sweep on either row) can drop the card while the
    // pointer still rests on it (erun-ui/playwright/AGENTS.md's hover-card
    // bullet).
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

      // Same fixed ch-based column (HOVER_CARD_GRID_CLASS) regardless of which
      // conditional rows this particular card happens to render.
      expect(mismatchLabelWidth).toBeCloseTo(plainLabelWidth, 0);
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  test('the card renders two zones, separated by a visible boundary', async ({ app }) => {
    let secondZoneBorder = '';
    let firstZoneBorder = '';
    // Hover and every read live inside one retryable block; see the
    // preceding test for why.
    await app.sidebar.readEnvHoverCard(SEED_TENANT, SEED_ENV_ALPHA, async (card) => {
      await expect(card).toBeVisible({ timeout: 1_000 });

      // Zone 1 (Version .. Working on) and zone 2 (Activity .. Cloud node) are
      // two separate `dl`s sharing the same grid template -- not one `dl` with
      // a spanning divider row -- so the count itself is part of the contract.
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

      // Live-state rows live in the second zone; identity rows -- Version,
      // Working on -- live in the first. The live-state zone's exact rows are
      // deliberately not asserted here: both of its conditional rows depend on
      // live state this test does not drive. The usage rows are `CPU`/`Memory`
      // once a reading is cached but the single degraded `Usage` row before
      // that (SEED_ENV_ALPHA is shared and another spec can leave a reading on
      // it), and the Cloud node row is omitted outright when there is no node.
      // Their own specs own those shapes -- sidebar-environment-usage.spec.ts
      // and sidebar-env-hover.spec.ts -- so this test stays on the two-zone
      // boundary it is named for.
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
    // Both sides are freshly-seeded, uniquely-named envs rather than the
    // shared SEED_ENV_ALPHA baseline row. SEED_ENV_ALPHA's zone 2 (Activity,
    // Usage, Cloud node) is not reset between specs -- another spec earlier
    // in the same worker (e.g. sidebar-environment-usage.spec.ts) can leave a
    // real Usage reading cached on it, stacking an age caption under the
    // headline that a pristine env's zone 2 never renders. Comparing against
    // that shared, mutable row made this assertion depend on suite ordering
    // instead of on the fixed layout it's meant to lock down.
    //
    // The comparison below is over zone 2's ROWS, not its height. UsageState
    // renders one line while the environment-usage sweep has no reading for
    // the env and two (headline + age caption) once it does, and a fresh env
    // starts unobserved -- the sweep runs on a 90s ticker and its first
    // reading for a freshly seeded env can therefore land between the two
    // reads below. That is a change of a value's line count, not of the
    // zone's row set, so it is not what this test is about: a height
    // comparison measures sweep timing as much as layout, and reported the
    // conditional row as "changing zone 2" when only the reading's arrival
    // had. What the Line mismatch row must not do is add, remove or move a
    // row in zone 2, which comparing the rows states directly.
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
        // The conditional row belongs to zone 1, not zone 2 -- read both so a
        // row that landed in the wrong zone cannot pass by being present
        // somewhere in the card.
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

  // A stale usage reading is degraded (muted, reduced opacity), never amber:
  // nothing the operator did caused it and no action follows from it, so it
  // must not look more alarming than an unmeasured number actually is (also
  // relevant to #1805 -- the percentage itself is not authoritative for a
  // build-capable environment).
  test('a stale usage reading renders degraded, not as an amber warning', async ({ app, page }) => {
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    // The CPU row, not the single `Usage` row: a cached reading renders CPU and
    // Memory as separate rows, and a stale reading is still a reading.
    const usageValue = card.locator('dt:text-is("CPU") + dd');
    let color = '';
    // Every read -- including the two that used to run after this block --
    // lives inside the one retryable attempt: the ongoing usage sweep can
    // re-emit and re-render the row between the card converging and a
    // subsequent un-retried read, dropping the card the same way a bare
    // sequence of asserts after a single hover would.
    await expect(async () => {
      await emitStaleEnvUsage(page, SEED_TENANT, SEED_ENV_ALPHA);
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('Stale', { timeout: 1_000 });
      // No alert icon -- a stale reading is not a warning.
      await expect(usageValue.locator('svg')).toHaveCount(0, { timeout: 1_000 });
      color = await usageValue
        .locator('span')
        .first()
        .evaluate((el) => window.getComputedStyle(el).color, undefined, { timeout: 1_000 });
    }).toPass({ timeout: 20_000 });

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

// The Usage figures are read from the runtime container's own cgroup, which
// is never where a build runs -- every build executes in the erun-dind
// sidecar instead, so the reading can look idle while that sidecar saturates
// the node (#1979). A build-capable environment's caption says so on every
// reading, not only while a build happens to be running -- there is no
// reliable "a build is running" signal that covers every way one can start
// (desktop, CLI, an MCP-driven orchestrator). A runtime-type environment has
// no dind sidecar at all (erun-devops/k8s/erun-devops/templates/service.yaml,
// `$dindEnabled`), so its reading is the whole story and must not carry the
// caveat.
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
      // The caption names the rows the caveat applies to rather than standing
      // alone: a Builds row of the sidecar's own now sits under CPU and Memory,
      // and a bare "excludes builds" beside it would read as qualifying that
      // row instead of them.
      await expect(card).toContainText('CPU and memory exclude builds', { timeout: 1_000 });
    }).toPass({ timeout: 20_000 });
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
      }).toPass({ timeout: 20_000 });
      await expect(card).not.toContainText('exclude builds');
      await expect(card).not.toContainText('erun-dind sidecar');
    } finally {
      removeEnvironment(SEED_TENANT, environment);
    }
  });

  // The reported defect, on the surface it was reported against: the popover
  // showed a build environment "Busy — holding: release 1.0.302" beside a CPU
  // of 0.2%, because the figure was the runtime container's own cgroup. The
  // work runs in the erun-dind sidecar, and the reader has always acquired that
  // second reading — so a card that draws only the runtime container's number
  // cannot tell a healthy build from a wedged one.
  test('a build-capable environment renders the sidecar as its own Builds row', async ({
    app,
    page,
  }) => {
    // The toPass retry (20s) nests hoverEnvironmentRow's own retry inside it, so
    // a single slow hover attempt under contention can consume most of the
    // default 30s test budget before the outer retry gets a second chance —
    // same reason sidebar-environment-usage.spec.ts's driven-hover cases widen
    // theirs. Widen this test rather than shrinking either nested bound.
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
      // The number that answers "is my build working", labelled so it cannot be
      // confused with the runtime container's 12.0% two rows above it.
      await expect(card.getByText('Builds', { exact: true })).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('91.5%', { timeout: 1_000 });
      await expect(card).toContainText('erun-dind sidecar · 19.4Gi of 20.0Gi', {
        timeout: 1_000,
      });
      // The caveat still applies to CPU and Memory, which is what it is for.
      await expect(card).toContainText('CPU and memory exclude builds', { timeout: 1_000 });
    }).toPass({ timeout: 20_000 });
  });

  // A sidecar with no cpu.max quota cannot report a percentage, and reporting
  // nothing would put the operator back where they started — a build-capable
  // environment whose only visible CPU figure is the runtime container's
  // near-zero. The cumulative counter is a real measurement and is stated as
  // CPU-seconds, with no decile strip: there is no ceiling to be a fraction of,
  // and an empty strip would read as "measured 0%".
  test('a sidecar with no CPU quota renders its cumulative CPU-seconds and no strip', async ({
    app,
    page,
  }) => {
    // The preceding case's comment applies here too: the nested hover retries
    // need more than the default test budget under contention.
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
      // Two strips — CPU and Memory — and none for the sidecar, whose reading
      // has no ceiling to be a fraction of.
      await expect(card.locator('[data-decile-fill]')).toHaveCount(2, { timeout: 1_000 });
    }).toPass({ timeout: 20_000 });
  });

  // The reported defect. An absent sidecar reading is a real state — an older
  // runtime image, a sidecar mid-restart — and not the same as an environment
  // that carries no sidecar. Both used to render as nothing, which left the
  // card showing the runtime container's near-idle CPU and memory qualified
  // only by the "excludes builds" caveat: a figure that reads as idle beside an
  // Activity line saying a build is running. The not-read state gets the row,
  // naming the container it could not reach.
  test('a build-capable environment whose sidecar could not be read says so instead of showing no Builds row', async ({
    app,
    page,
  }) => {
    // The same nested-hover budget the cases above widen for.
    test.setTimeout(60_000);
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(async () => {
      // No reading supplied, but the environment carries the sidecar: exactly
      // what the reader emits when its exec into the sidecar fails.
      await emitEnvUsage(page, SEED_TENANT, SEED_ENV_ALPHA, 5, 90, undefined, true);
      await page.mouse.move(0, 0);
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      await expect(card).toBeVisible({ timeout: 1_000 });
      await expect(card.getByText('Builds', { exact: true })).toBeVisible({ timeout: 1_000 });
      await expect(card).toContainText('erun-dind sidecar', { timeout: 1_000 });
      await expect(card).toContainText('the sidecar could not be read', { timeout: 1_000 });
      // Not-read is not a reading: no strip for it, unlike the measured CPU and
      // memory above — and still not the zero the card must never fabricate.
      await expect(card.locator('[data-decile-fill]')).toHaveCount(2, { timeout: 1_000 });
    }).toPass({ timeout: 20_000 });
  });
});
