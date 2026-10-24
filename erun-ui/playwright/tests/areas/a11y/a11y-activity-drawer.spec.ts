import AxeBuilder from '@axe-core/playwright';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

async function emitActivity(
  page: import('@playwright/test').Page,
  entry: Record<string, unknown>,
): Promise<void> {
  await page.evaluate((e) => {
    (
      window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
    ).runtime.EventsEmit('activity:state', e);
  }, entry);
}

function runningEntry(id: string): Record<string, unknown> {
  return {
    id,
    command: 'open',
    tenant: 'petios',
    environment: 'rihards-review',
    status: 'running',
    startedAt: new Date(0).toISOString(),
    lastUpdated: new Date(0).toISOString(),
    source: 'action',
    actionKind: 'open',
    summary: 'open petios/rihards-review',
  };
}

test.describe('activity drawer accessibility', () => {
  test('Escape closes the drawer, unmounts it, and returns focus to the launcher', async ({
    app,
    page,
  }) => {
    await app.activityDrawer.open();
    await expect(app.activityDrawer.locator()).toBeVisible();
    await expect
      .poll(
        () => page.evaluate(() => document.activeElement?.closest('[role="dialog"]') !== null),
        withTestBudget(),
      )
      .toBe(true);

    await page.keyboard.press('Escape');

    await expect(app.activityDrawer.locator()).toHaveCount(0, withTestBudget());
    await expect(app.activityDrawer.launcher()).toBeFocused(withTestBudget());
  });

  test('closing unmounts every control, so none is reachable by Tab while closed', async ({
    app,
  }) => {
    await app.activityDrawer.open();
    await expect(app.activityDrawer.closeButton()).toBeVisible();

    await app.activityDrawer.close();

    await expect(app.activityDrawer.locator()).toHaveCount(0, withTestBudget());
    await expect(app.activityDrawer.closeButton()).toHaveCount(0, withTestBudget());
  });

  test('a card states its status as visible text, not only a hidden icon', async ({
    app,
    page,
  }) => {
    await emitActivity(page, runningEntry('a11y-status-1'));
    await app.activityDrawer.open();

    const card = app.activityDrawer
      .locator()
      .locator('article')
      .filter({ hasText: 'rihards-review' });
    // waitFor defers to the enclosing test's budget.
    await card.getByText('Running', { exact: true }).waitFor({ state: 'visible' });
    await expect(card.getByText('Running', { exact: true })).toBeVisible();
  });

  test('the live announcer reports a status transition, not the elapsed clock', async ({
    app,
    page,
  }) => {
    await emitActivity(page, runningEntry('a11y-status-2'));
    await app.activityDrawer.open();

    // The sr-only announcer, not the header count live region.
    const announcer = app.activityDrawer.locator().locator('.sr-only[role="status"]');
    await emitActivity(page, { ...runningEntry('a11y-status-2'), status: 'succeeded' });

    await expect(announcer).toContainText('petios/rihards-review Succeeded', withTestBudget());
    // The announcer must never read like the elapsed clock ("3s", "1m4s").
    await expect(announcer).not.toHaveText(/^\d+s$|^\d+m\d+s$/);
  });

  // Scoped to one rule; a whole-page scan would flag unrelated findings.
  test('axe reports no aria-hidden-focus violations with the drawer open', async ({
    app,
    page,
  }) => {
    await app.activityDrawer.open();
    const results = await new AxeBuilder({ page }).withRules(['aria-hidden-focus']).analyze();
    expect(results.violations).toEqual([]);
  });
});
