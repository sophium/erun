import AxeBuilder from '@axe-core/playwright';

import { expect, test } from '../fixtures/erunApp.js';

// The activity drawer was a plain <div> toggling `aria-hidden` and a
// CSS transform, so it was never a real dialog -- Escape did nothing, closing
// it left every control (including Kill, which can terminate a running
// deploy) sitting focusable inside an aria-hidden subtree, and one aria-live
// region wrapped the whole card list, re-announcing each card's per-second
// elapsed clock. It now renders on Radix's Dialog primitive, which gives focus
// trap, Escape-to-close, and focus restoration for free, plus a narrow
// announcer that only fires on a real status transition.

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
    // Radix's focus trap puts initial focus inside the dialog on open.
    await expect
      .poll(() => page.evaluate(() => document.activeElement?.closest('[role="dialog"]') !== null))
      .toBe(true);

    await page.keyboard.press('Escape');

    await expect(app.activityDrawer.locator()).toHaveCount(0);
    await expect(app.activityDrawer.launcher()).toBeFocused();
  });

  test('closing unmounts every control, so none is reachable by Tab while closed', async ({
    app,
  }) => {
    await app.activityDrawer.open();
    await expect(app.activityDrawer.closeButton()).toBeVisible();

    await app.activityDrawer.close();

    await expect(app.activityDrawer.locator()).toHaveCount(0);
    await expect(app.activityDrawer.closeButton()).toHaveCount(0);
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
    await expect(card.getByText('Running', { exact: true })).toBeVisible();
  });

  test('the live announcer reports a status transition, not the elapsed clock', async ({
    app,
    page,
  }) => {
    await emitActivity(page, runningEntry('a11y-status-2'));
    await app.activityDrawer.open();

    // Scoped to the sr-only announcer only (not the header's "N now/next"
    // count, which is also a live region but never carries an elapsed clock).
    const announcer = app.activityDrawer.locator().locator('.sr-only[role="status"]');
    await emitActivity(page, { ...runningEntry('a11y-status-2'), status: 'succeeded' });

    await expect(announcer).toContainText('petios/rihards-review Succeeded');
    // The elapsed clock renders as e.g. "3s" / "1m4s" -- the announcer's text
    // must never take that shape, or a screen reader is back to hearing a tick.
    await expect(announcer).not.toHaveText(/^\d+s$|^\d+m\d+s$/);
  });

  // Scoped to the one rule the old always-mounted-and-aria-hidden drawer
  // violated (Kill and friends stayed focusable behind aria-hidden while
  // "closed"); a whole-page scan would also flag unrelated, pre-existing
  // findings elsewhere in the app that this issue doesn't cover.
  test('axe reports no aria-hidden-focus violations with the drawer open', async ({
    app,
    page,
  }) => {
    await app.activityDrawer.open();
    const results = await new AxeBuilder({ page }).withRules(['aria-hidden-focus']).analyze();
    expect(results.violations).toEqual([]);
  });
});
