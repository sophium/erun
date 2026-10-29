import type { Page, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Every element in both hover cards renders at 10px in the shared sans face; emphasis comes only
// from colour, weight or state.

// The card lives only while the pointer rests on its row, so reads are bounded and batched; a card
// closing mid-read hangs rather than fails.
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
      // Wait for the stubbed working-issue fetch so every row is rendered before sampling.
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
      // A card closing mid-read yields NaN from a detached element; keep it inside the retry.
      expect(Number.isFinite(titleSize)).toBe(true);
      expect(Number.isFinite(titleWeight)).toBe(true);
      expect(Number.isFinite(valueWeight)).toBe(true);
    }).toPass();

    expect(valueSizes.size, 'every dd in the card must share one font-size').toBe(1);
    expect(labelSizes.size, 'every dt in the card must share one font-size').toBe(1);

    const [valueSize] = [...valueSizes];
    const [labelSize] = [...labelSizes];
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
      // See the preceding test.
      expect(Number.isFinite(titleSize)).toBe(true);
      expect(Number.isFinite(titleWeight)).toBe(true);
      expect(Number.isFinite(valueWeight)).toBe(true);
    }).toPass();

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
      // tabular-nums is inherited from the card container.
      const variant = await version
        .locator('span')
        .first()
        .evaluate((el) => window.getComputedStyle(el).fontVariantNumeric, undefined, {
          timeout: READ_TIMEOUT_MS,
        });
      expect(variant).toContain('tabular-nums');
    }).toPass();
  });
});
