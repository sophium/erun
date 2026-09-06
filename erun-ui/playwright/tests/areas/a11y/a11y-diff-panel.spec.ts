import AxeBuilder from '@axe-core/playwright';
import type { Locator, Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// The diff panel had exactly zero focusable scrollers -- the vertical
// diff content area and every hunk's horizontal scroller had no tabIndex, so
// a keyboard-only reviewer could not scroll past the first screenful of any
// diff or read a line wider than the panel (axe scrollable-region-focusable,
// WCAG 2.1.1). The resize handles were <button>s wired only to onMouseDown, so
// Enter/Space/arrow keys did nothing either. These specs drive real keyboard
// input (Tab, Escape, Arrow keys) and assert the resulting focus/layout state,
// not just the markup.

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

// theme.css only ever declares --ring/--background as achromatic
// `oklch(L 0 0)` (no chroma/hue), so lightness alone determines relative
// luminance. `getComputedStyle` in this browser preserves the oklch()
// notation rather than downgrading it to rgb(), so the ratio is computed
// directly from the OKLCH value instead of relying on a resolved sRGB string.
function achromaticOklchLightness(value: string): number {
  // The browser normalizes the declared `oklch(0.6 0 0)` to `oklch(60% 0 0)`.
  const match = /oklch\(\s*([\d.]+)(%?)\s+0\s+0\s*\)/.exec(value);
  if (!match?.[1]) {
    throw new Error(`expected an achromatic oklch() value, got: ${value}`);
  }
  const lightness = Number(match[1]);
  return match[2] === '%' ? lightness / 100 : lightness;
}

// The OKLab -> linear sRGB matrix (https://bottosson.github.io/posts/oklab/)
// reduces to r = g = b = lightness^3 when chroma/hue are zero (each row's
// coefficients sum to 1), and WCAG relative luminance weights (0.2126,
// 0.7152, 0.0722) also sum to 1 -- so for this theme's achromatic tokens,
// relative luminance is just lightness^3.
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

// Opening the seeded env schedules its own terminal-focus cascade
// (`focusTerminalSoon`'s immediate + rAF + delayed calls), independent of
// anything this spec does, and it can still be in flight when a test reaches
// for the resize handle -- landing an Arrow key on the terminal instead of
// the handle. Retrying the whole focus+press against the actual value (not a
// fixed delay) converges once that cascade has run its course.
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
    await expect
      .poll(() => app.reviewPanel.diffSectionPaths())
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

    // A real click focuses it exactly like Tab landing on it would (browsers
    // refuse to focus an element with no tabindex on click, so this alone
    // already exercises the fix); the follow-up Tab presses are a genuine
    // keyboard traversal to the next stops in the DOM. Click near the
    // region's top padding, not its center -- the center can land inside the
    // nested hunk scroller for a single short diff. Retried as a whole: the
    // env-open flow schedules its own terminal-focus cascade that can steal
    // focus back between the click and the Tab presses.
    //
    // The per-environment section header's own "Start a review" button
    // (DiffList.StartReviewAction.tsx, #1412) is a real, keyboard-reachable
    // control that renders between the region and this file's hunk, so it is
    // a genuine tab stop of its own -- one Tab reaches it, a second reaches
    // the hunk. This was already true before this test's own regression
    // check was written, and had gone unnoticed until keyboard navigation
    // began actually stepping through this exact sequence (#1421).
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

    // The ARIA value is not decorative: the actual CSS var the layout reads
    // must have moved with it.
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

  // axe cannot see keyboard behavior, but it can catch the two structural
  // defects the issue named directly: a scrollable region with no way into
  // the tab order, and a focusable control sitting inside an aria-hidden
  // subtree. Scoped to these rules (not a whole-page scan) so the assertion
  // tracks this surface's own regressions, not unrelated pre-existing
  // findings elsewhere in the app.
  test('axe reports no scrollable-region-focusable or aria-hidden-focus violations', async ({
    page,
  }) => {
    const results = await new AxeBuilder({ page })
      .withRules(['scrollable-region-focusable', 'aria-hidden-focus'])
      .analyze();
    expect(results.violations).toEqual([]);
  });
});
