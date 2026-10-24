import type { Page, Response } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// WCAG 2.2 SC 1.4.13: the pointer can move onto tooltip content without it
// disappearing. Asserted as the outcome, not as a particular prop.

const SHORTCUT_LABEL = 'Select all orchestrators';

// awaitIdlePolls waits for `count` successive LoadIdleStatus polls, outlasting
// the exit animation during which a released tooltip still matches.
async function awaitIdlePolls(page: Page, count: number): Promise<void> {
  const isIdleStatus = (response: Response): boolean =>
    response.url().includes('/__erun_invoke') &&
    (response.request().postData() ?? '').includes('LoadIdleStatus');
  for (let poll = 0; poll < count; poll += 1) {
    await page.waitForResponse(isIdleStatus);
  }
}

async function pointerOverTooltipContent(
  page: Page,
  point: { x: number; y: number },
): Promise<boolean> {
  return page.evaluate(({ x, y }) => {
    const content = document.querySelector('[data-slot="tooltip-content"]');
    const under = document.elementFromPoint(x, y);
    return content !== null && under !== null && content.contains(under);
  }, point);
}

test('the pointer can be moved onto a group shortcut’s tooltip content', async ({ app, page }) => {
  await app.titlebar.openWhipPanel();

  const shortcut = app.titlebar
    .whipPanel()
    .getByRole('button', { name: SHORTCUT_LABEL, exact: true });
  const tooltip = app.page.getByRole('tooltip', { name: SHORTCUT_LABEL, exact: true });

  await shortcut.hover();
  await expect(tooltip).toBeVisible();

  const box = await tooltip.boundingBox();
  expect(box).not.toBeNull();
  const centre = {
    x: (box?.x ?? 0) + (box?.width ?? 0) / 2,
    y: (box?.y ?? 0) + (box?.height ?? 0) / 2,
  };

  await app.page.mouse.move(centre.x, centre.y, { steps: 4 });

  await awaitIdlePolls(page, 2);

  // The content is still there and the pointer is on it.
  await expect.poll(() => pointerOverTooltipContent(app.page, centre), withTestBudget()).toBe(true);
  await expect(tooltip).toBeVisible(withTestBudget());
});
