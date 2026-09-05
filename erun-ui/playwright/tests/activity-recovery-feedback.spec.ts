import { expect, test } from '../fixtures/erunApp.js';

// RecoveryFeedback used to render its outer <section role="status"> regardless
// of outcome, so a failed recovery -- a write the backend refused -- was
// announced no more urgently than a success. It now routes a failure
// through InlineAlert (role="alert"), matched here via the accessibility tree
// rather than an attribute string match.
//
// The synthetic entry below is never pushed into the backend's real activity
// queue (it only exists in the frontend's Redux state via EventsEmit), so
// clicking "Clear pending helm release" hits App.resolveRecoverableHelmEntry's
// id-not-found guard and returns deterministically before touching any real
// helm/kubectl binary -- unlike "Run doctor"/"Rebuild & redeploy" on the same
// card, which pipe real commands into the shared Local shell and must not be
// clicked in this harness (see activity-failure-details.spec.ts).
test('a failed pending-helm recovery announces as an alert, not a status update', async ({
  app,
  page,
}) => {
  await app.activityDrawer.open();
  await expect(app.activityDrawer.locator()).toBeVisible();

  await page.evaluate(() => {
    const now = new Date().toISOString();
    const entry = {
      id: 'recovery-alert-1',
      command: 'deploy',
      tenant: 'petios',
      environment: 'rihards-recovery',
      version: '1.0.80',
      release: 'petios-devops',
      namespace: 'petios-rihards-recovery',
      status: 'failed',
      startedAt: now,
      endedAt: now,
      lastUpdated: now,
      source: 'trace',
      error: '==> Deploy failed after 4s',
    };
    (
      window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
    ).runtime.EventsEmit('activity:state', entry);
  });

  const drawer = app.activityDrawer.locator();
  const card = drawer.locator('article').filter({ hasText: 'petios/rihards-recovery' }).first();
  const recoverButton = card.getByRole('button', { name: 'Clear pending helm release' });
  await expect(recoverButton).toBeVisible();
  await recoverButton.click();

  const alert = drawer.getByRole('alert').filter({ hasText: 'Recovery failed' });
  await expect(alert).toBeVisible();
  await expect(alert).toContainText('activity not found');
  // The drawer's other role="status" region (the live status announcer) must
  // not also claim this message -- it is an alert, not a status update.
  await expect(drawer.getByRole('status').filter({ hasText: 'Recovery failed' })).toHaveCount(0);

  await page.screenshot({ path: 'test-results/recovery-feedback-alert-light.png' });
  // The theme toggle button lives in the titlebar, behind the drawer's own
  // overlay while open, and the recovery feedback is ephemeral component
  // state that a close/reopen would lose -- so the dark capture flips the
  // same `.dark` class the toggle button applies (root AGENTS.md's
  // Design-Language Decision Record: both apps share one class-based
  // light/dark mechanism) directly, without exercising the toggle control.
  await page.evaluate(() => {
    document.documentElement.classList.add('dark');
  });
  await page.screenshot({ path: 'test-results/recovery-feedback-alert-dark.png' });
});
