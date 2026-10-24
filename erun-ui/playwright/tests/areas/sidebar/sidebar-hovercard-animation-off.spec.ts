import type { Locator } from '@playwright/test';

import { disablePopoverEntranceAnimation, expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// Pins disablePopoverEntranceAnimation's contract against the rendered popover.
// Checks the animation is present first so the assertion can't pass vacuously.
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
