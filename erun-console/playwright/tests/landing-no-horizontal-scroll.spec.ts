import { expect, test, type Page } from '@playwright/test';

// Needs a real browser: jsdom has no layout, so scrollWidth is always 0 there.

// The hero glow's -2rem inset bleeds past the 24px section padding below ~504px.
const PHONE_WIDTHS = [320, 375, 414, 480];

const GLOW = '[data-testid="landing-hero-glow"]';

// Reduced motion: the hero's entrance translate would otherwise widen the scroll region mid-animation.
async function openLanding(page: Page, width: number, height = 812): Promise<void> {
  await page.setViewportSize({ width, height });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/');
  await expect(page.locator(GLOW)).toBeAttached();
}

async function horizontalOverflow(
  page: Page,
): Promise<{ scrollWidth: number; clientWidth: number; innerWidth: number }> {
  return await page.evaluate(() => {
    const de = document.documentElement;
    return {
      scrollWidth: de.scrollWidth,
      clientWidth: de.clientWidth,
      innerWidth: window.innerWidth,
    };
  });
}

for (const width of PHONE_WIDTHS) {
  test(`the landing page does not scroll horizontally at ${width}px`, async ({ page }) => {
    await openLanding(page, width);
    const { scrollWidth, clientWidth, innerWidth } = await horizontalOverflow(page);

    // clientWidth excludes the vertical scrollbar headless Chromium reserves.
    expect(scrollWidth - clientWidth).toBe(0);
    expect(scrollWidth).toBeLessThanOrEqual(innerWidth);
  });
}

test('the hero glow still renders and still bleeds past its parent at desktop widths', async ({
  page,
}) => {
  await openLanding(page, 1280, 900);
  const glow = page.locator(GLOW);
  const measured = await glow.evaluate((el) => {
    const parent = el.parentElement;
    if (parent === null) {
      throw new Error('the hero glow is expected to sit inside the hero visual');
    }
    const glowBox = el.getBoundingClientRect();
    const parentBox = parent.getBoundingClientRect();
    return {
      glow: {
        left: glowBox.left,
        right: glowBox.right,
        width: glowBox.width,
        height: glowBox.height,
      },
      parent: { left: parentBox.left, right: parentBox.right },
      filter: getComputedStyle(el).filter,
    };
  });

  // The fix must keep the glow and its bleed, not delete or shrink it.
  expect(measured.glow.width).toBeGreaterThan(0);
  expect(measured.glow.height).toBeGreaterThan(0);
  expect(measured.glow.left).toBeLessThan(measured.parent.left);
  expect(measured.glow.right).toBeGreaterThan(measured.parent.right);
  expect(measured.filter).toContain('blur');

  const { scrollWidth, clientWidth } = await horizontalOverflow(page);
  expect(scrollWidth - clientWidth).toBe(0);
});
