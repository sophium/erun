import type { Page, Response } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// The env hover card is held open by two independent owners -- the pointer
// resting on the row and keyboard focus sitting inside it. An env row is a
// button, so clicking it or tabbing to it focuses it while the pointer is
// still parked on it; both owners are live at once all the time.
//
// This spec pins the case that used to lose the card for good: focus departs
// the row for anywhere at all, with the pointer unmoved. Both owners drove one
// `open` boolean through one pair of callbacks, so a blur released the card
// exactly like a mouseleave and closed it 120ms later -- and nothing reopened
// it, because reopening takes a fresh mouseenter and the pointer was already
// inside. The card vanished under a stationary pointer and stayed gone until
// the operator jiggled the mouse.
test.describe('sidebar env hover card holds through focus loss', () => {
  test('focus leaving the row does not drop a card the pointer still rests on', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const row = app.sidebar.envRowButton(tenant, environment);
    const card = app.sidebar.envHoverCard(tenant, environment);

    // Open the env so the idle-status poll is live, and take one poll's
    // response as proof of that: the "still open" window below is measured in
    // those polls, so a run where they never fire has to fail here rather than
    // stall later.
    await app.sidebar.openEnvironment(tenant, environment);
    await awaitIdlePolls(page, 1);

    // CONTROL. Hover the row and focus it -- the operator's own two ways of
    // raising this card -- then leave both alone. Nothing releases the card, so
    // it must simply stay. This is what makes the probe below attributable: the
    // card can survive several idle polls while the pointer is parked here.
    await app.sidebar.hoverEnvironmentRow(tenant, environment);
    await row.focus();
    await awaitIdlePolls(page, 2);
    await expect(card).toBeVisible(withTestBudget());

    // PROBE. Focus departs to nowhere in particular, exactly as it does when
    // the operator tabs away or into another surface. The pointer does not
    // move, and nothing re-hovers.
    await row.blur();

    // The window is bounded by real app events, never a sleep: the idle poll
    // re-arms itself 1s after each completion
    // (TerminalController.scheduleIdleStatusPoll), so two successive polls
    // cannot fit inside the 120ms close grace. A card that closed on the blur
    // has already closed by the time the second lands.
    await awaitIdlePolls(page, 2);

    // The pointer never moved, so "the pointer left" cannot explain a missing
    // card. Asserted before the card so a failure names which half broke.
    await expect
      .poll(() => row.evaluate((el) => el.matches(':hover')), withTestBudget())
      .toBe(true);

    // The reproduction. Re-hovering is deliberately absent: re-parking the
    // pointer is how the suite's other hover specs recover a dropped card, and
    // it hides this defect rather than reproducing it.
    await expect(card).toBeVisible(withTestBudget());

    await app.sidebar.closeEnvironment(tenant, environment);
  });
});

// awaitIdlePolls waits for `count` successive LoadIdleStatus round trips, each
// request armed only after the previous response lands, so every one of them is
// a poll the one before it scheduled rather than a burst answering one trigger.
async function awaitIdlePolls(page: Page, count: number): Promise<void> {
  const isIdleStatus = (response: Response): boolean =>
    response.url().includes('/__erun_invoke') &&
    (response.request().postData() ?? '').includes('LoadIdleStatus');
  for (let poll = 0; poll < count; poll += 1) {
    await page.waitForResponse(isIdleStatus);
  }
}
