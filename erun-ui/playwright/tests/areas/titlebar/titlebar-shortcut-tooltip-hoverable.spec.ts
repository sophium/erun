import type { Page, Response } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// WCAG 2.2 SC 1.4.13 "Content on Hover or Focus" (AA) requires that pointer
// hover can raise the additional content AND that the pointer can then be moved
// over that content without the content disappearing. A tooltip built on
// Radix's `disableHoverableContent` keeps neither half of the second
// requirement: it releases the content as the pointer leaves the trigger, so
// the label can only be read from a distance and never from under the pointer.
//
// This spec pins the requirement itself for the whip panel's three group
// shortcuts, which sit side by side and are the tooltips an operator is most
// likely to reach for while their hand is already on the mouse. It is phrased
// as the outcome the success criterion describes rather than in terms of any
// particular prop, so a fix by a different route passes it too.

const SHORTCUT_LABEL = 'Select all orchestrators';

// awaitIdlePolls waits for `count` successive LoadIdleStatus round trips, each
// request armed only after the previous response lands, so every one of them is
// a poll the one before it scheduled rather than a burst answering one trigger.
//
// This is the bounded window the assertion below needs, and reading without it
// reports a released tooltip as held: a Radix tooltip sets its close state in
// the same task as the pointerleave, then stays in the document, still carrying
// role="tooltip" and still matching :hover, for the length of its exit
// animation. A caller that looks the moment the pointer lands therefore sees an
// element that is on its way out and calls it present. Successive polls are
// ~1s apart against that animation, and both are real app events rather than a
// sleep.
async function awaitIdlePolls(page: Page, count: number): Promise<void> {
  const isIdleStatus = (response: Response): boolean =>
    response.url().includes('/__erun_invoke') &&
    (response.request().postData() ?? '').includes('LoadIdleStatus');
  for (let poll = 0; poll < count; poll += 1) {
    await page.waitForResponse(isIdleStatus);
  }
}

// pointerOverTooltipContent reports whether the element under `point` is inside
// a tooltip's content, which is what "the pointer is over the content" means in
// the document rather than in a screenshot.
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

  // The move onto the content, in the shape an operator makes it: off the
  // trigger and onto the label, with the pointer coming to rest there.
  await app.page.mouse.move(centre.x, centre.y, { steps: 4 });

  await awaitIdlePolls(page, 2);

  // Both halves of the criterion, asserted together: the content is still
  // there, and the pointer is on it rather than on a hole it left behind.
  await expect.poll(() => pointerOverTooltipContent(app.page, centre), withTestBudget()).toBe(true);
  await expect(tooltip).toBeVisible(withTestBudget());
});
