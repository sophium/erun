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
  ).toBeVisible({ timeout: 5000 });
});
