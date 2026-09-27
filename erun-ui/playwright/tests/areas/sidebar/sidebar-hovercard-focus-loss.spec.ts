import type { Locator, Page, Response } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';

// The env hover card is held open by two independent owners -- the pointer
// resting on the row and keyboard focus sitting inside it. An env row is a
// button, so clicking it or tabbing to it focuses it while the pointer is
// still parked on it; both owners are live at once all the time.
//
// This spec pins the case that used to lose the card for good: focus departs
// the row, with the pointer unmoved. Both owners drove one `open` boolean
// through one pair of callbacks, so a blur released the card exactly like a
// mouseleave and closed it 120ms later -- and nothing reopened it, because
// reopening takes a fresh mouseenter and the pointer was already inside. The
// card vanished under a stationary pointer and stayed gone until the operator
// jiggled the mouse.
//
// Focus departs by more than one road, and they do not reach the card the same
// way. That is why there are two cases here rather than one, and why the first
// one alone was not enough:
//
//   - `blur()` takes focus to nowhere -- the body. No `focusin` follows it, so
//     the only release is the row's own `onBlurCapture`, and the card lives or
//     dies by how that release is coordinated.
//   - A Tab press takes focus to another control, which fires `focusin`
//     somewhere else in the document. The popover's own dismissable layer reads
//     that as a dismissal and closes the card itself, through a road that never
//     consults the coordination at all -- so a spec that only ever blurs passes
//     while a single Tab still drops the card.
//
// A third case carries the two bounds those leave open. Its Shift+Tab steps
// off the row's own controls rather than onto the next one, so the pointer is
// demonstrably the only holder left -- which is what makes it evidence that the
// pointer alone holds the card, not just that some holder did. And it then
// releases that last holder, because holding a card open is only correct while
// a holder remains: a fix that simply refused every close would pass the two
// cases above and wedge the card on screen.
test.describe('sidebar env hover card holds through focus loss', () => {
  test('focus leaving the row does not drop a card the pointer still rests on', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const row = app.sidebar.envRowButton(tenant, environment);
    const card = app.sidebar.envHoverCard(tenant, environment);

    await holdCardUnderParkedPointer(app, page, row, tenant, environment);

    // PROBE. Focus departs to nowhere in particular, exactly as it does when
    // the operator clicks a blank part of the window. The pointer does not
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

  test('tabbing off the row does not drop a card the pointer still rests on', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const row = app.sidebar.envRowButton(tenant, environment);
    const card = app.sidebar.envHoverCard(tenant, environment);

    await holdCardUnderParkedPointer(app, page, row, tenant, environment);

    // PROBE. One Tab press -- the operator's own way of leaving a row for the
    // next control, with the pointer left exactly where it was. This is the
    // gesture the blur-only case above cannot reach: it moves focus somewhere
    // real rather than nowhere, which is what gives the popover's own dismiss
    // path something to act on.
    await page.keyboard.press('Tab');

    // Focus has to have landed on an actual control for this probe to have
    // driven anything. A Tab that reached nothing leaves focus on the body,
    // which is the case pinned above -- so without this the test could report
    // a reproduction it never exercised.
    await expect.poll(() => focusedControl(page), withTestBudget()).not.toBe(null);
    await expect
      .poll(() => row.evaluate((el) => el === document.activeElement), withTestBudget())
      .toBe(false);

    // Same bounded window as the blur case, from the same real events.
    await awaitIdlePolls(page, 2);

    await expect
      .poll(() => row.evaluate((el) => el.matches(':hover')), withTestBudget())
      .toBe(true);
    await expect(card).toBeVisible(withTestBudget());

    await app.sidebar.closeEnvironment(tenant, environment);
  });

  test('focus leaving the row for good leaves the card held by the pointer alone', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const row = app.sidebar.envRowButton(tenant, environment);
    const card = app.sidebar.envHoverCard(tenant, environment);

    await holdCardUnderParkedPointer(app, page, row, tenant, environment);

    // PROBE. Shift+Tab, the other half of the operator's own way of leaving a
    // row. Unlike the forward Tab above it steps off the row's own controls
    // outright, which is what this case is for: with no focus left inside the
    // row, the pointer is the ONLY holder still holding, so a card still up
    // afterwards is one the pointer held by itself.
    await page.keyboard.press('Shift+Tab');

    // The row's anchor div wraps the row's controls, so :focus-within on it is
    // exactly "focus is still somewhere in this row". Asserted, because a focus
    // move that stayed inside the row is re-held by the row's own focus holder
    // and would say nothing about the pointer.
    await expect
      .poll(
        () => row.evaluate((el) => el.parentElement?.matches(':focus-within') ?? true),
        withTestBudget(),
      )
      .toBe(false);

    await awaitIdlePolls(page, 2);

    await expect
      .poll(() => row.evaluate((el) => el.matches(':hover')), withTestBudget())
      .toBe(true);
    await expect(card).toBeVisible(withTestBudget());

    // The bound on all three cases, from the last holder's own road: the
    // pointer leaving is a release like any other, so with nothing left to
    // hold it the card closes. A fix that simply refused to close would pass
    // the cases above and wedge the card on screen here.
    await page.mouse.move(0, 0);
    await expect
      .poll(() => row.evaluate((el) => el.matches(':hover')), withTestBudget())
      .toBe(false);
    await awaitIdlePolls(page, 2);
    await expect(card).toBeHidden(withTestBudget());

    await app.sidebar.closeEnvironment(tenant, environment);
  });
});

// holdCardUnderParkedPointer raises the card the operator's own two ways --
// pointer resting on the row, focus sitting inside it -- and converges on the
// card being up. Both holders are in place and nothing moves them afterwards,
// which is what makes the probes below attributable: the card demonstrably
// survives idle polls here, so a card missing later was released by the probe
// and not by drift.
async function holdCardUnderParkedPointer(
  app: AppShell,
  page: Page,
  row: Locator,
  tenant: string,
  environment: string,
): Promise<void> {
  // Open the env so the idle-status poll is live, and take one poll's response
  // as proof of that: the windows below are measured in those polls, so a run
  // where they never fire has to fail here rather than stall later.
  await app.sidebar.openEnvironment(tenant, environment);
  await awaitIdlePolls(page, 1);
  await app.sidebar.hoverEnvironmentRow(tenant, environment);
  await row.focus();
  await awaitIdlePolls(page, 2);
  await expect(app.sidebar.envHoverCard(tenant, environment)).toBeVisible(withTestBudget());
}

// focusedControl names whatever currently holds focus, or null when that is
// nothing at all (the body).
async function focusedControl(page: Page): Promise<string | null> {
  return page.evaluate(() => {
    const active = document.activeElement;
    if (active === null || active === document.body) {
      return null;
    }
    return active.tagName;
  });
}

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
