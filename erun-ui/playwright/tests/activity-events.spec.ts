import { expect, test } from '../fixtures/erunApp.js';

test('activity:state events populate the queue drawer', async ({ app }) => {
  await app.activityDrawer.open();
  await expect(app.activityDrawer.locator()).toBeVisible();

  await app.page.evaluate(() => {
    const entry = {
      id: 'fake-1',
      command: 'open',
      tenant: 'petios',
      environment: 'rihards-review',
      status: 'running',
      startedAt: new Date().toISOString(),
      lastUpdated: new Date().toISOString(),
      source: 'action',
      actionKind: 'open',
      summary: 'open petios/rihards-review',
    };
    (
      window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
    ).runtime.EventsEmit('activity:state', entry);
  });

  await expect(
    app.activityDrawer.locator().getByText('open petios/rihards-review', { exact: false }),
  ).toBeVisible();
});

// The launcher is named for what the drawer holds (every recovery-relevant
// action: init/build/release/open/deploy), not for one command among them
// (#1218 — it was previously labelled "Open deploy queue" with a Rocket icon).
test('activity launcher is labelled for the drawer it opens, not one command', async ({ app }) => {
  await expect(app.activityDrawer.launcher()).toHaveAttribute('aria-label', 'Open activities');

  await app.page.evaluate(() => {
    const ts = new Date().toISOString();
    (
      window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
    ).runtime.EventsEmit('activity:state', {
      id: 'launcher-label-spec-1',
      command: 'init',
      tenant: 'launcher-label-spec',
      environment: 'env-a',
      status: 'running',
      startedAt: ts,
      lastUpdated: ts,
      source: 'action',
      actionKind: 'init',
      summary: 'init launcher-label-spec/env-a',
    });
  });

  await expect(app.activityDrawer.launcher()).toHaveAttribute(
    'aria-label',
    'Open activities (1 active)',
  );
});
