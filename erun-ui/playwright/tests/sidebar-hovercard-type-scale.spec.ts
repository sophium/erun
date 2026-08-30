import type { Page, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ORCHESTRATOR, SEED_TENANT } from '../fixtures/seedRoot.js';

// #1694: neither sidebar hover card had a declared type scale -- the env card
// alone mixed three sizes and two faces across a four-row card, and its value
// column carried three different treatments on its own (mono-12px, sans-12px,
// and unclassed-inherits-14px). Both cards now render every label/value row
// through the shared HoverCardRow (Sidebar.HoverCardRow.tsx), which fixes the
// scale to exactly three treatments: a 12px muted label/caption, a 14px value
// (mono only as a face choice on identifiers, never a size change), and a
// 10px badge pill. This spec asserts the scale computationally rather than by
// reading pixel counts off a screenshot, so a future one-off className on a
// single row fails a test instead of drifting back to six treatments quietly.

async function fontSizePx(locator: import('@playwright/test').Locator): Promise<number> {
  const size = await locator.evaluate((el) => window.getComputedStyle(el).fontSize);
  return Number.parseFloat(size);
}

async function distinctFontSizes(
  locator: import('@playwright/test').Locator,
): Promise<Set<number>> {
  const count = await locator.count();
  const sizes = new Set<number>();
  for (let index = 0; index < count; index += 1) {
    sizes.add(await fontSizePx(locator.nth(index)));
  }
  return sizes;
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

test.describe('sidebar hover card type scale (#1694)', () => {
  test('the env card value column is exactly one font-size, distinct from the label size', async ({
    app,
    page,
  }) => {
    await stubWorkingIssue(page);
    await app.reboot();

    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(card).toBeVisible();
    // Wait for the working-issue fetch (stubbed above) to resolve so the
    // mono branch value is actually rendered before sizes are sampled.
    await expect(card).toContainText('feature/1694-hover-card-type-scale');

    const values = distinctFontSizes(card.locator('dd'));
    const labels = distinctFontSizes(card.locator('dt'));
    const [valueSizes, labelSizes] = await Promise.all([values, labels]);

    expect(valueSizes.size, 'every dd in the card must share one font-size').toBe(1);
    expect(labelSizes.size, 'every dt in the card must share one font-size').toBe(1);

    const [valueSize] = [...valueSizes];
    const [labelSize] = [...labelSizes];
    expect(valueSize).toBeGreaterThan(labelSize as number);

    // The title reuses the value treatment's size (emphasis is by weight
    // only), never a fourth size.
    const title = card.getByText(`${SEED_TENANT} / ${SEED_ENV_ALPHA}`, { exact: true });
    expect(await fontSizePx(title)).toBeCloseTo(valueSize as number, 1);
  });

  test('the orchestrator card value column is exactly one font-size, distinct from the label size', async ({
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

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();
    const card = app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR);
    await expect(card).toBeVisible();
    await expect(card).toContainText('Working, for');

    const values = distinctFontSizes(card.locator('dd'));
    const labels = distinctFontSizes(card.locator('dt'));
    const [valueSizes, labelSizes] = await Promise.all([values, labels]);

    expect(valueSizes.size, 'every dd in the card must share one font-size').toBe(1);
    expect(labelSizes.size, 'every dt in the card must share one font-size').toBe(1);

    const [valueSize] = [...valueSizes];
    const [labelSize] = [...labelSizes];
    expect(valueSize).toBeGreaterThan(labelSize as number);

    const title = card.getByText(SEED_ORCHESTRATOR, { exact: true });
    expect(await fontSizePx(title)).toBeCloseTo(valueSize as number, 1);
  });

  test('numeric values (version, usage figures) carry tabular figures', async ({ app }) => {
    await app.reboot();
    await app.sidebar.hoverEnvironmentRow(SEED_TENANT, SEED_ENV_ALPHA);
    const card = app.sidebar.envHoverCard(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(card).toBeVisible();

    const version = card.locator('dd').first();
    await expect(version).toContainText('1.0.0');
    const variant = await version
      .locator('span')
      .first()
      .evaluate((el) => window.getComputedStyle(el).fontVariantNumeric);
    expect(variant).toContain('tabular-nums');
  });
});
