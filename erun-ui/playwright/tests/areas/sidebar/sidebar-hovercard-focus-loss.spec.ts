import type { Locator, Page, Response } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';

// The card is held by the pointer and by focus; losing focus with the pointer parked must not close
// it.
// blur() releases via onBlurCapture; Tab triggers the popover's own dismiss; a third case proves
// the pointer alone holds it and its release closes it.
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

    // PROBE: focus goes to the body; the pointer does not move.
    await row.blur();

    // Idle polls re-arm 1s apart, so two cannot fit inside the 120ms close grace.
    await awaitIdlePolls(page, 2);

    // Asserted before the card so a failure names which half broke.
    await expect
      .poll(() => row.evaluate((el) => el.matches(':hover')), withTestBudget())
      .toBe(true);

    // No re-hover: that would hide the defect.
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

    // PROBE: Tab moves focus to a real control, reaching the popover's dismiss path.
    await page.keyboard.press('Tab');

    // A Tab that reached nothing would just repeat the blur case.
    await expect.poll(() => focusedControl(page), withTestBudget()).not.toBe(null);
    await expect
      .poll(() => row.evaluate((el) => el === document.activeElement), withTestBudget())
      .toBe(false);

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

    // PROBE: Shift+Tab leaves the row entirely, so the pointer is the only holder.
    await page.keyboard.press('Shift+Tab');

    // A focus move that stayed inside the row would say nothing about the pointer.
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

    // With the last holder gone the card must close; refusing every close would fail here.
    await page.mouse.move(0, 0);
    await expect
      .poll(() => row.evaluate((el) => el.matches(':hover')), withTestBudget())
      .toBe(false);
    await awaitIdlePolls(page, 2);
    await expect(card).toBeHidden(withTestBudget());

    await app.sidebar.closeEnvironment(tenant, environment);
  });
});

// Raises the card with both holders and converges, so a later missing card is the probe's doing.
async function holdCardUnderParkedPointer(
  app: AppShell,
  page: Page,
  row: Locator,
  tenant: string,
  environment: string,
): Promise<void> {
  // Probe windows are measured in idle polls, so fail here if they never fire.
  await app.sidebar.openEnvironment(tenant, environment);
  await awaitIdlePolls(page, 1);
  await app.sidebar.hoverEnvironmentRow(tenant, environment);
  await row.focus();
  await awaitIdlePolls(page, 2);
  await expect(app.sidebar.envHoverCard(tenant, environment)).toBeVisible(withTestBudget());
}

async function focusedControl(page: Page): Promise<string | null> {
  return page.evaluate(() => {
    const active = document.activeElement;
    if (active === null || active === document.body) {
      return null;
    }
    return active.tagName;
  });
}

// Each request is armed only after the previous response, so these are successive polls.
async function awaitIdlePolls(page: Page, count: number): Promise<void> {
  const isIdleStatus = (response: Response): boolean =>
    response.url().includes('/__erun_invoke') &&
    (response.request().postData() ?? '').includes('LoadIdleStatus');
  for (let poll = 0; poll < count; poll += 1) {
    await page.waitForResponse(isIdleStatus);
  }
}
