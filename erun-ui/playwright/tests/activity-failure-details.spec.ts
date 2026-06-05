import { expect, test } from '../fixtures/erunApp.js';

// Pins the deploy-failure-details surface (issue #430): a failed deploy card
// exposes the captured command output behind a "Show output" disclosure and a
// "Copy failure report" action, so the user can see why a deploy failed and
// hand the evidence to developers/admins instead of facing a bare one-line
// summary. Clipboard reads are restricted in the headless harness, so we assert
// the observable button-state flip ("Copied") rather than inspecting the
// clipboard; the report-string assembly and the backend output capture are
// covered by the Go tests in activity_queue_test.go / activity_queue_app_test.go.
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

  // The one-line summary stays first-class.
  await expect(
    drawer.getByText('==> Deploy failed after 4s', { exact: false }).first(),
  ).toBeVisible({ timeout: 5000 });

  // Captured output is collapsed by default and revealed via the disclosure.
  // "UPGRADE FAILED" lives only in the captured output, never in the summary.
  await expect(drawer.getByText('UPGRADE FAILED', { exact: false })).toHaveCount(0);
  await drawer.getByRole('button', { name: 'Show output' }).click();
  await expect(drawer.getByText('UPGRADE FAILED', { exact: false })).toBeVisible();

  // The copy action is present and confirms the copy without depending on
  // clipboard read permissions (copyToClipboard swallows unavailable-API
  // errors, so the "Copied" flip is the reliable observable signal).
  const copyButton = drawer.getByRole('button', { name: 'Copy failure report' });
  await expect(copyButton).toBeVisible();
  await copyButton.click();
  await expect(drawer.getByRole('button', { name: 'Copied' })).toBeVisible();
});
