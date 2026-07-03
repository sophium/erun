import { expect, test } from '../fixtures/erunApp.js';

// A failed deploy card must let the user see why the deploy failed and copy a
// shareable evidence report, not just a bare one-line summary. Report-string
// assembly and backend output capture are covered by the Go tests in
// activity_queue_test.go / activity_queue_app_test.go.
test('failed deploy card reveals captured output and offers a copyable report', async ({ app }) => {
  await app.activityDrawer.open();
  await expect(app.activityDrawer.locator()).toBeVisible();

  await app.page.evaluate(() => {
    const now = new Date().toISOString();
    const entry = {
      id: 'fail-1',
      command: 'deploy',
      tenant: 'petios',
      environment: 'rihards-develop',
      version: '1.0.80',
      release: 'petios-devops',
      namespace: 'petios-rihards-develop',
      status: 'failed',
      startedAt: now,
      endedAt: now,
      lastUpdated: now,
      source: 'trace',
      error: '==> Deploy failed after 4s',
      detail:
        'helm upgrade --install petios-devops ./chart\n' +
        'Error: UPGRADE FAILED: timed out waiting for the condition\n' +
        '==> Deploy failed after 4s',
    };
    (
      window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
    ).runtime.EventsEmit('activity:state', entry);
  });

  const drawer = app.activityDrawer.locator();

  // The singleton backend keeps failed activities from earlier specs in its
  // history, so the locator must scope to this synthetic entry's card rather
  // than matching controls across cards.
  const card = drawer.locator('article').filter({ hasText: 'petios/rihards-develop' }).first();

  // The one-line summary stays first-class.
  await expect(card.getByText('==> Deploy failed after 4s', { exact: false })).toBeVisible();

  // "UPGRADE FAILED" lives only in the captured output, never in the summary.
  await expect(card.getByText('UPGRADE FAILED', { exact: false })).toHaveCount(0);
  await card.getByRole('button', { name: 'Show output' }).click();
  await expect(card.getByText('UPGRADE FAILED', { exact: false })).toBeVisible();

  // Clipboard reads are unavailable in the headless harness, and copyToClipboard
  // swallows the unavailable-API error, so the "Copied" flip is the only
  // observable signal that the copy fired.
  const copyButton = card.getByRole('button', { name: 'Copy failure report' });
  await expect(copyButton).toBeVisible();
  await copyButton.click();
  await expect(card.getByRole('button', { name: 'Copied' })).toBeVisible();

  // Failed deploy/open cards offer recovery actions; the clear-pending-helm one
  // renders only because this entry carries a release + namespace. We assert
  // they render rather than click — clicking would run real deploy/helm flows
  // the headless harness must not, and each action's backend method has its own
  // test coverage.
  await expect(card.getByRole('button', { name: 'Run doctor' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Rebuild & redeploy' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Clear pending helm release' })).toBeVisible();
});
