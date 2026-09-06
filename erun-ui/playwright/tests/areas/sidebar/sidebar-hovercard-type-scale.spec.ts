import type { Page, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// #1901 replaced the three-treatment scale this spec used to lock (a 12px
// label/caption, a 14px value, a 10px badge) with a stricter invariant: one
// size, one face, always -- every element in both sidebar hover cards
// (Sidebar.HoverCardRow.tsx) renders at 10px in the shared sans face, and an
// element earns emphasis only through colour, weight or state, never a
// second size or face. This spec asserts that computationally rather than by
// reading pixel counts off a screenshot, so a future one-off className on a
// single row fails a test instead of drifting the scale back open.

// The card is only alive while the pointer rests on the row that raised it, so
// every read below is bounded and taken in as few round trips as possible: a
// sample split across one call per element leaves a window between counting
// the rows and measuring them, and a card that closes inside that window does
// not fail the read, it hangs it -- the next .nth() waits for an element that
// cannot come back while the pointer still sits where it is. The callers
// re-hover and re-read as one convergent step for the same reason.
const READ_TIMEOUT_MS = 2_000;

async function fontSizePx(locator: import('@playwright/test').Locator): Promise<number> {
  const size = await locator.evaluate((el) => window.getComputedStyle(el).fontSize, undefined, {
    timeout: READ_TIMEOUT_MS,
  });
  return Number.parseFloat(size);
}

async function fontWeight(locator: import('@playwright/test').Locator): Promise<number> {
  const weight = await locator.evaluate((el) => window.getComputedStyle(el).fontWeight, undefined, {
    timeout: READ_TIMEOUT_MS,
  });
  return Number.parseFloat(weight);
}

async function distinctFontSizes(
  locator: import('@playwright/test').Locator,
): Promise<Set<number>> {
  const sizes = await locator.evaluateAll((els) =>
    els.map((el) => Number.parseFloat(window.getComputedStyle(el).fontSize)),
  );
  return new Set(sizes);
}

async function stubWorkingIssue(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'EnvironmentWorkingIssue') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            available: true,
            branch: 'feature/1694-hover-card-type-scale',
            issueNumber: 1694,
            issueTitle: 'Neither sidebar hover card has a type scale',
          },
        }),
      });
    }
    await route.continue();
  });
}

test.describe('sidebar hover card type scale (#1694, #1901)', () => {
  test('the env card renders one size everywhere, with the title distinguished by weight alone', async ({
    app,
    page,
  }) => {
    await stubWorkingIssue(page);
    await app.reboot();

    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    let valueSizes = new Set<number>();
    let labelSizes = new Set<number>();
    let titleSize = 0;
    let titleWeight = 0;
    let valueWeight = 0;
    await expect(async () => {
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      // Wait for the working-issue fetch (stubbed above) to resolve so every
      // row -- including the branch value -- is actually rendered before
      // sizes are sampled.
      await expect(card).toContainText('feature/1694-hover-card-type-scale', {
        timeout: READ_TIMEOUT_MS,
      });
      valueSizes = await distinctFontSizes(card.locator('dd'));
      labelSizes = await distinctFontSizes(card.locator('dt'));
      const title = card.getByText(`${SEED_TENANT} / ${SEED_ENV_ALPHA}`, { exact: true });
      titleSize = await fontSizePx(title);
      titleWeight = await fontWeight(title);
      valueWeight = await fontWeight(card.locator('dd').first());
      expect(valueSizes.size).toBeGreaterThan(0);
      expect(labelSizes.size).toBeGreaterThan(0);
      // A card that closes mid-read (the pointer never left, so nothing
      // reopens it -- see hoverEnvironmentRow) can leave title/weight reads
      // resolving against a detached element, whose computed style comes
      // back as an unparseable empty string (NaN here) rather than
      // throwing. Guarding only the two Sets above let a NaN escape this
      // retry and fail the assertions below unconditionally on the first
      // bad read, with no further retry.
      expect(Number.isFinite(titleSize)).toBe(true);
      expect(Number.isFinite(titleWeight)).toBe(true);
      expect(Number.isFinite(valueWeight)).toBe(true);
    }).toPass({ timeout: 20_000 });

    expect(valueSizes.size, 'every dd in the card must share one font-size').toBe(1);
    expect(labelSizes.size, 'every dt in the card must share one font-size').toBe(1);

    const [valueSize] = [...valueSizes];
    const [labelSize] = [...labelSizes];
    // One size everywhere: label, value and title all match -- no element
    // earns emphasis by growing, only by weight/colour/state.
    expect(valueSize).toBe(labelSize);
    expect(titleSize).toBeCloseTo(valueSize as number, 1);
    expect(titleWeight).toBeGreaterThan(valueWeight);
  });

  test('the orchestrator card renders one size everywhere, with the title distinguished by weight alone', async ({
    app,
    page,
  }) => {
    await page.route('**/__erun_invoke', async (route: Route, request) => {
      const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (parsed.method === 'ListOrchestrators') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: [
              {
                id: SEED_ORCHESTRATOR,
                name: SEED_ORCHESTRATOR,
                environments: [
                  { tenant: SEED_TENANT, environment: SEED_ENV_ALPHA, directory: '/tmp/a' },
                ],
                tenants: [],
                directories: [],
                sessionId: 4242,
                status: 'running',
                busy: true,
                busyAtUnix: Math.floor(Date.now() / 1000) - 60,
                transient: true,
                shellRunning: false,
                shellCommand: '',
                shellStartedAtUnix: 0,
              },
            ],
          }),
        });
      }
      await route.continue();
    });
    await app.reboot();

    const card = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);
    let valueSizes = new Set<number>();
    let labelSizes = new Set<number>();
    let titleSize = 0;
    let titleWeight = 0;
    let valueWeight = 0;
    await expect(async () => {
      await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
      await expect(card).toContainText('Working, for', { timeout: READ_TIMEOUT_MS });
      valueSizes = await distinctFontSizes(card.locator('dd'));
      labelSizes = await distinctFontSizes(card.locator('dt'));
      const title = card.getByText(SEED_ORCHESTRATOR, { exact: true });
      titleSize = await fontSizePx(title);
      titleWeight = await fontWeight(title);
      valueWeight = await fontWeight(card.locator('dd').first());
      expect(valueSizes.size).toBeGreaterThan(0);
      expect(labelSizes.size).toBeGreaterThan(0);
      // See the preceding test for why: guard against a NaN escaping this
      // retry from a card that closed mid-read.
      expect(Number.isFinite(titleSize)).toBe(true);
      expect(Number.isFinite(titleWeight)).toBe(true);
      expect(Number.isFinite(valueWeight)).toBe(true);
    }).toPass({ timeout: 20_000 });

    expect(valueSizes.size, 'every dd in the card must share one font-size').toBe(1);
    expect(labelSizes.size, 'every dt in the card must share one font-size').toBe(1);

    const [valueSize] = [...valueSizes];
    const [labelSize] = [...labelSizes];
    expect(valueSize).toBe(labelSize);
    expect(titleSize).toBeCloseTo(valueSize as number, 1);
    expect(titleWeight).toBeGreaterThan(valueWeight);
  });

  test('numeric values (version, usage figures) carry tabular figures from the card container down', async ({
    app,
  }) => {
    await app.reboot();
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(async () => {
      await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
      const version = card.locator('dd').first();
      await expect(version).toContainText('1.0.0', { timeout: READ_TIMEOUT_MS });
      // tabular-nums is declared once on the card's wrapping container
      // (#1901), not per value -- font-variant-numeric inherits, so the
      // version span itself carries no class of its own and still reads
      // tabular through inheritance.
      const variant = await version
        .locator('span')
        .first()
        .evaluate((el) => window.getComputedStyle(el).fontVariantNumeric, undefined, {
          timeout: READ_TIMEOUT_MS,
        });
      expect(variant).toContain('tabular-nums');
    }).toPass({ timeout: 20_000 });
  });
});
