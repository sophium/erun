import { expect, test, type Page } from '@playwright/test';

// The console's unauthenticated front door, measured in a real engine. The
// app's Vitest suite runs in jsdom, which implements no layout whatsoever --
// `documentElement.scrollWidth` is always 0 there, so that suite cannot
// observe this class of bug even in principle. The boundary this spec proves
// is CSS layout, so it needs a real browser and nothing else: the landing
// screen renders from bundled defaults with no token, and platform discovery
// failing (there is deliberately no API behind this suite) is exactly the
// "fall back, don't hard-fail" path `fetchPlatformConfig` already defines.
const gated = process.env.ERUN_E2E_CONSOLE_LANDING_LAYOUT !== '1';

// The hero visual's brand-tinted glow is inset -2rem on every side. Below
// ~504px the visual spans the full content width, so its right-hand bleed
// exceeds the section's 24px padding by 8px and widens the document's scroll
// region at every phone width. 320 and 375 are the widths it was measured at.
const PHONE_WIDTHS = [320, 375, 414, 480];

const GLOW = '[data-testid="landing-hero-glow"]';

// The landing screen renders only once auth resolution settles; the glow
// itself is the observable condition that it has, so nothing here waits on a
// wall-clock sleep. Reduced motion keeps the measurement on the settled
// layout: the hero's entrance animation is a translate, and a transformed
// element contributes its transformed box to the scroll region, so a
// mid-animation read would be measuring something else.
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
    test.skip(
      gated,
      'opt-in: set ERUN_E2E_CONSOLE_LANDING_LAYOUT=1 (./run-landing-layout.sh builds and serves the bundle, then sets it)',
    );

    await openLanding(page, width);
    const { scrollWidth, clientWidth, innerWidth } = await horizontalOverflow(page);

    // scrollWidth against clientWidth is the measurement this bug was reported
    // with. Unlike a comparison against innerWidth it is unaffected by the
    // width of the vertical scrollbar, which headless Chromium does reserve.
    expect(scrollWidth - clientWidth).toBe(0);
    expect(scrollWidth).toBeLessThanOrEqual(innerWidth);
  });
}

test('the hero glow still renders and still bleeds past its parent at desktop widths', async ({
  page,
}) => {
  test.skip(
    gated,
    'opt-in: set ERUN_E2E_CONSOLE_LANDING_LAYOUT=1 (./run-landing-layout.sh builds and serves the bundle, then sets it)',
  );

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

  // The whole point of the element is a soft blurred wash bleeding outside the
  // card it sits behind, so the assertions below are what keep the horizontal
  // fix honest: one that deletes the glow, or shrinks it back inside its
  // parent's box, fails here rather than passing as "no more overflow".
  expect(measured.glow.width).toBeGreaterThan(0);
  expect(measured.glow.height).toBeGreaterThan(0);
  expect(measured.glow.left).toBeLessThan(measured.parent.left);
  expect(measured.glow.right).toBeGreaterThan(measured.parent.right);
  expect(measured.filter).toContain('blur');

  // The fix must not introduce overflow at desktop widths either.
  const { scrollWidth, clientWidth } = await horizontalOverflow(page);
  expect(scrollWidth - clientWidth).toBe(0);
});
