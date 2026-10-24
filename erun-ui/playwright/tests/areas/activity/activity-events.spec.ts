import type { Page } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

interface ActivityEntry {
  id: string;
  tenant: string;
  environment: string;
  status: string;
  command: string;
}

function runningEntry(entry: ActivityEntry): Record<string, unknown> {
  const ts = new Date().toISOString();
  return {
    ...entry,
    startedAt: ts,
    lastUpdated: ts,
    source: 'action',
    actionKind: entry.command,
    summary: `${entry.command} ${entry.tenant}/${entry.environment}`,
  };
}

function emitActivityState(page: Page, entry: Record<string, unknown>): Promise<void> {
  return page.evaluate((payload) => {
    (
      window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
    ).runtime.EventsEmit('activity:state', payload);
  }, entry);
}

test('activity:state events populate the queue drawer', async ({ app, page }) => {
  await app.activityDrawer.open();
  await expect(app.activityDrawer.locator()).toBeVisible();

  await emitActivityState(
    page,
    runningEntry({
      id: 'fake-1',
      command: 'open',
      tenant: 'petios',
      environment: 'rihards-review',
      status: 'running',
    }),
  );

  const row = app.activityDrawer
    .locator()
    .getByText('open petios/rihards-review', { exact: false });
  await row.waitFor({ state: 'visible' });
  await expect(row).toBeVisible();
});

test('activity launcher is labelled for the drawer it opens, not one command', async ({
  app,
  page,
}) => {
  await expect(app.activityDrawer.launcher()).toHaveAttribute('aria-label', 'Open activities');

  await emitActivityState(
    page,
    runningEntry({
      id: 'launcher-label-spec-1',
      command: 'init',
      tenant: 'launcher-label-spec',
      environment: 'env-a',
      status: 'running',
    }),
  );

  await expect(app.activityDrawer.launcher()).toHaveAttribute(
    'aria-label',
    'Open activities (1 active)',
    withTestBudget(),
  );
});

// Must outlast expect.timeout (10s on POSIX) so the hold reliably disagrees with it.
const EMIT_HOLD_MS = 12_000;

// Must exceed EMIT_HOLD_MS plus the surrounding steps.
const HELD_EMIT_SCENARIO_BUDGET_MS = 60_000;

// EventsEmit is a POST to /__erun_emit that the backend rebroadcasts over SSE, so
// holding it holds the render. The frontend emits no events of its own.
async function holdActivityState(page: Page): Promise<void> {
  await page.route('**/__erun_emit', async (route) => {
    if ((route.request().postData() ?? '').includes('activity:state')) {
      // Deliberate stimulus, sized against EMIT_HOLD_MS.
      await new Promise<void>((resolve) => setTimeout(resolve, EMIT_HOLD_MS));
    }
    await route.continue();
  });
}

test('the drawer row converges when the activity event carrying it is held past the old bound', async ({
  app,
  page,
}) => {
  test.setTimeout(HELD_EMIT_SCENARIO_BUDGET_MS);
  await app.activityDrawer.open();
  await holdActivityState(page);

  await emitActivityState(
    page,
    runningEntry({
      id: 'held-row-1',
      command: 'open',
      tenant: 'held-emit',
      environment: 'held-row',
      status: 'running',
    }),
  );

  const row = app.activityDrawer.locator().getByText('open held-emit/held-row', { exact: false });
  // No explicit timeout, so the hold is absorbed by the test's budget.
  await row.waitFor({ state: 'visible' });
  await expect(row).toBeVisible();
});

test('the launcher count converges when the activity event carrying it is held past the old bound', async ({
  app,
  page,
}) => {
  test.setTimeout(HELD_EMIT_SCENARIO_BUDGET_MS);
  // Kept shut: an open drawer aria-hides the launcher from getByRole.
  await expect(app.activityDrawer.launcher()).toHaveAttribute('aria-label', 'Open activities');
  await holdActivityState(page);

  await emitActivityState(
    page,
    runningEntry({
      id: 'held-launcher-1',
      command: 'open',
      tenant: 'held-emit-launcher',
      environment: 'held-launcher',
      status: 'running',
    }),
  );

  await expect(app.activityDrawer.launcher()).toHaveAttribute(
    'aria-label',
    'Open activities (1 active)',
    withTestBudget(),
  );
});
