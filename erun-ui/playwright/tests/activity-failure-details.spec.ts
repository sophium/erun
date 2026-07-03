import { expect, test } from '../fixtures/erunApp.js';

// Pins the deploy-failure-details surface: a failed deploy card
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

  // Scope to this entry's own card. Earlier specs in the suite can leave
  // failed activities in the singleton backend's history (each rendering its
  // own "Show output"/"Copy failure report" controls), so we must not match
  // across cards. "petios/rihards-develop 1.0.80" is unique to the synthetic
  // entry above.
  const card = drawer.locator('article').filter({ hasText: 'petios/rihards-develop' }).first();

  // The one-line summary stays first-class.
  await expect(card.getByText('==> Deploy failed after 4s', { exact: false })).toBeVisible();

  // Captured output is collapsed by default and revealed via the disclosure.
  // "UPGRADE FAILED" lives only in the captured output, never in the summary.
  await expect(card.getByText('UPGRADE FAILED', { exact: false })).toHaveCount(0);
  await card.getByRole('button', { name: 'Show output' }).click();
  await expect(card.getByText('UPGRADE FAILED', { exact: false })).toBeVisible();

  // The copy action is present and confirms the copy without depending on
  // clipboard read permissions (copyToClipboard swallows unavailable-API
  // errors, so the "Copied" flip is the reliable observable signal).
  const copyButton = card.getByRole('button', { name: 'Copy failure report' });
  await expect(copyButton).toBeVisible();
  await copyButton.click();
  await expect(card.getByRole('button', { name: 'Copied' })).toBeVisible();

  // Failed deploy/open cards offer the "select a fix" recovery actions:
  // troubleshoot via the deploy-aware doctor, force a clean rebuild + redeploy,
  // or clear a stuck pending helm release (shown because the entry carries a
  // release + namespace). We assert they render rather than click them:
  // clicking triggers real backend deploy/helm flows the headless harness must
  // not run, and the Wails methods (StartDoctorSession / StartForceDeploySession
  // / recoverPendingHelm) are exercised by their own backend paths.
  await expect(card.getByRole('button', { name: 'Run doctor' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Rebuild & redeploy' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Clear pending helm release' })).toBeVisible();
});
