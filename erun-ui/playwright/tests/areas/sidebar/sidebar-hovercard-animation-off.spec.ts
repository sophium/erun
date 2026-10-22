import type { Locator } from '@playwright/test';

import { disablePopoverEntranceAnimation, expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// disablePopoverEntranceAnimation (fixtures/erunApp.ts) exists so a hover
// card's geometry is read at rest instead of mid-entrance. Every spec that
// calls it depends on that being literally true, and a helper that quietly
// stopped neutralising the animation would leave those specs measuring a
// transitioning card while their assertions went on passing most of the time.
// This pins the helper's own contract against the rendered popover rather
// than trusting that the CSS it injects still matches the component.
//
// The direction is what gives the assertion meaning: PopoverContent carries
// `data-[state=open]:animate-in ... zoom-in-95`, so its computed
// animation-name is the entrance animation until the helper removes it. A
// test that only checked the post-helper state would pass vacuously against a
// popover that never animated in the first place.
async function animationState(
  card: Locator,
): Promise<{ animationName: string; transform: string }> {
  return card.evaluate((el) => {
    const style = getComputedStyle(el);
    return { animationName: style.animationName, transform: style.transform };
  });
}

test.describe('hover card entrance animation', () => {
  test('the shared helper stops the popover entrance animation on the rendered card', async ({
    app,
    page,
  }) => {
    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible();
      const before = await animationState(card);
      expect(before.animationName).not.toBe('none');
    });

    await disablePopoverEntranceAnimation(page);

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible();
      const after = await animationState(card);
      expect(after.animationName).toBe('none');
      expect(after.transform).toBe('none');
    });
  });
});
