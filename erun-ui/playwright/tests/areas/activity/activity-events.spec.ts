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
  // waitFor (not expect) converges against the enclosing test's own budget
  // rather than the event's render racing expect's fixed one.
  await row.waitFor({ state: 'visible' });
  await expect(row).toBeVisible();
});

// The launcher is named for what the drawer holds (every recovery-relevant
// action: init/build/release/open/deploy), not for one command among them
// (#1218 — it was previously labelled "Open deploy queue" with a Rocket icon).
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

  // withTestBudget, not expect's 10s default: the launcher's label is a count
  // the app recomputes from the event above, so this converges on the test's
  // own budget like every other wait here.
  await expect(app.activityDrawer.launcher()).toHaveAttribute(
    'aria-label',
    'Open activities (1 active)',
    withTestBudget(),
  );
});

// How long the held-emit cases below hold the activity event for. The clock it
// has to disagree with is the one the pre-fix convergence inherited: `expect`
// and `expect.poll` with no explicit timeout resolve to `expect.timeout`
// (playwright.config.ts sets 10s on POSIX), and that deadline is a give-up
// point rather than a lower bound. 12s clears it with room for the CDP round
// trip between the poll's clock starting and the route handler seeing the POST,
// and still lands well inside the budget each step now runs on -- so the cases
// pass on the fixed code for the right reason. A shorter hold stops reliably
// disagreeing with the clock it exists to disagree with.
const EMIT_HOLD_MS = 12_000;

// The budget each held-emit case declares. It must exceed EMIT_HOLD_MS plus the
// drawer open and the assertions around it, or the scenario would expire around
// its own inner step -- the same defect these cases prove, seen from the other
// end.
const HELD_EMIT_SCENARIO_BUDGET_MS = 60_000;

// holdActivityState holds the render path for a staged activity event.
//
// The staging write is a POST: runtime.EventsEmit is fetch('/__erun_emit') in
// the headless shim, and the backend re-broadcasts it down the /__erun_events
// SSE stream to the app's own listener. Holding that POST therefore holds the
// render the assertions converge on, which forces the overlap a contended gate
// produced by luck. The frontend emits no events of its own, so filtering on
// the event name keeps the hold off everything else.
async function holdActivityState(page: Page): Promise<void> {
  await page.route('**/__erun_emit', async (route) => {
    if ((route.request().postData() ?? '').includes('activity:state')) {
      // Deliberate stimulus, not a wait for the app: this hold *is* the
      // contention the cases exist to reproduce, so it is sized on the clock it
      // has to disagree with (see EMIT_HOLD_MS).
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
  // No explicit timeout: this defers to the budget the test declared above, so
  // the hold is absorbed rather than reported as a missing row.
  await row.waitFor({ state: 'visible' });
  await expect(row).toBeVisible();
});

test('the launcher count converges when the activity event carrying it is held past the old bound', async ({
  app,
  page,
}) => {
  test.setTimeout(HELD_EMIT_SCENARIO_BUDGET_MS);
  // The drawer stays shut: the launcher count is fed by the app-level queue
  // listener, and an open drawer puts the launcher behind Radix's aria-hidden
  // subtree, where getByRole cannot see it.
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
