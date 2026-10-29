import AxeBuilder from '@axe-core/playwright';
import type { Locator, Page } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

interface DiffLineStub {
  kind: string;
  oldLine: number | null;
  newLine: number | null;
  content: string;
}
interface DiffFileStub {
  path: string;
  status: string;
  additions: number;
  deletions: number;
  binary: boolean;
  hunks: { header: string; lines: DiffLineStub[] }[];
}

function diffFile(path: string, lineContent: string): DiffFileStub {
  return {
    path,
    status: 'modified',
    additions: 1,
    deletions: 0,
    binary: false,
    hunks: [
      {
        header: '@@ -1,1 +1,1 @@',
        lines: [{ kind: 'add', oldLine: null, newLine: 1, content: lineContent }],
      },
    ],
  };
}

async function stubDiff(page: Page, files: DiffFileStub[]): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadDiff') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            rawDiff: '',
            workingDirectory: '/seed',
            summary: { fileCount: files.length, additions: 1, deletions: 0 },
            files,
            tree: files.map((f) => ({ name: f.path, path: f.path, type: 'file', depth: 0 })),
            scope: 'current',
            includesWorktree: true,
          },
        }),
      });
    }
    await route.continue();
  });
}

// Theme tokens are achromatic oklch and getComputedStyle keeps oklch notation,
// so contrast is computed from lightness directly.
function achromaticOklchLightness(value: string): number {
  // The browser normalizes `oklch(0.6 0 0)` to `oklch(60% 0 0)`.
  const match = /oklch\(\s*([\d.]+)(%?)\s+0\s+0\s*\)/.exec(value);
  if (!match?.[1]) {
    throw new Error(`expected an achromatic oklch() value, got: ${value}`);
  }
  const lightness = Number(match[1]);
  return match[2] === '%' ? lightness / 100 : lightness;
}

// With zero chroma, OKLab -> linear sRGB gives r = g = b = L^3, so luminance is L^3.
function relativeLuminanceFromAchromaticOklch(lightness: number): number {
  return lightness ** 3;
}

function contrastRatio(lightnessA: number, lightnessB: number): number {
  const lighter = Math.max(
    relativeLuminanceFromAchromaticOklch(lightnessA),
    relativeLuminanceFromAchromaticOklch(lightnessB),
  );
  const darker = Math.min(
    relativeLuminanceFromAchromaticOklch(lightnessA),
    relativeLuminanceFromAchromaticOklch(lightnessB),
  );
  return (lighter + 0.05) / (darker + 0.05);
}

// Opening the env schedules a terminal-focus cascade that can steal the Arrow key;
// retry focus+press until the value moves.
async function stepResizeHandle(
  page: Page,
  handle: Locator,
  key: string,
  target: number,
): Promise<void> {
  await expect(async () => {
    const current = Number(await handle.getAttribute('aria-valuenow'));
    if (current === target) return;
    await handle.focus();
    await page.keyboard.press(key);
    expect(Number(await handle.getAttribute('aria-valuenow'))).toBe(target);
  }).toPass();
}

test.describe('diff panel accessibility', () => {
  test.beforeEach(async ({ app, page, seededEnv }) => {
    await stubDiff(page, [diffFile('src/very/long/path/wide-file.ts', 'x'.repeat(400))]);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    await expect
      .poll(() => app.reviewPanel.diffSectionPaths(), withTestBudget())
      .toEqual(['src/very/long/path/wide-file.ts']);
  });

  test('the diff content region and a hunk scroller are real Tab stops, in order', async ({
    app,
    page,
  }) => {
    const region = app.reviewPanel.diffContentRegion();
    const hunk = app.reviewPanel.hunkRegion('src/very/long/path/wide-file.ts');
    await expect(region).toHaveAttribute('tabindex', '0');
    await expect(hunk).toHaveAttribute('tabindex', '0');

    // Click near the top padding (the centre can hit a nested hunk scroller); retried because
    // the env-open focus cascade can steal focus. The section's "Start a review" button is
    // a tab stop before the hunk.
    await expect(async () => {
      await region.click({ position: { x: 5, y: 5 } });
      await expect(region).toBeFocused();
      await page.keyboard.press('Tab');
      await expect(app.page.getByRole('button', { name: 'Start a review' })).toBeFocused();
      await page.keyboard.press('Tab');
      await expect(hunk).toBeFocused();
    }).toPass();
  });

  test('the diff resize handle is keyboard-adjustable and reports its value', async ({
    app,
    page,
  }) => {
    const handle = app.reviewPanel.resizeHandle();
    await expect(handle).toHaveAttribute('role', 'slider');
    await expect(handle).toHaveAttribute('aria-orientation', 'vertical');
    const before = Number(await handle.getAttribute('aria-valuenow'));
    expect(before).toBeGreaterThan(0);

    await stepResizeHandle(page, handle, 'ArrowRight', before + 16);
    await stepResizeHandle(page, handle, 'ArrowLeft', before);
    await stepResizeHandle(page, handle, 'ArrowLeft', before - 16);

    // The CSS var the layout reads must move with the ARIA value.
    const width = await page.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue('--review-width').trim(),
    );
    expect(width).toBe(`${String(before - 16)}px`);
  });

  test('the focus ring meets the 3:1 non-text contrast floor against the background', async ({
    page,
  }) => {
    const [ring, background] = await page.evaluate(() => {
      const root = getComputedStyle(document.documentElement);
      return [root.getPropertyValue('--ring').trim(), root.getPropertyValue('--background').trim()];
    });
    const ratio = contrastRatio(
      achromaticOklchLightness(ring),
      achromaticOklchLightness(background),
    );
    expect(ratio).toBeGreaterThanOrEqual(3);
  });

  // Scoped to these rules; a whole-page scan would flag unrelated findings.
  test('axe reports no scrollable-region-focusable or aria-hidden-focus violations', async ({
    page,
  }) => {
    const results = await new AxeBuilder({ page })
      .withRules(['scrollable-region-focusable', 'aria-hidden-focus'])
      .analyze();
    expect(results.violations).toEqual([]);
  });
});
