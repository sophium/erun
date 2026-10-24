import { artifactPath } from '../../../fixtures/artifacts.js';
import { expect, test } from '../../../fixtures/erunApp.js';

// The synthetic entry exists only in frontend state, so the recovery hits the backend's
// id-not-found guard without touching real helm/kubectl.
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
  await recoverButton.waitFor({ state: 'visible' });
  await expect(recoverButton).toBeVisible();
  await recoverButton.click();

  const alert = drawer.getByRole('alert').filter({ hasText: 'Recovery failed' });
  await alert.waitFor({ state: 'visible' });
  await expect(alert).toBeVisible();
  await expect(alert).toContainText('activity not found');
  await expect(drawer.getByRole('status').filter({ hasText: 'Recovery failed' })).toHaveCount(0);

  await page.screenshot({ path: artifactPath('test-results/recovery-feedback-alert-light.png') });
  // The theme toggle is behind the drawer overlay and reopening loses the feedback,
  // so flip the `.dark` class directly.
  await page.evaluate(() => {
    document.documentElement.classList.add('dark');
  });
  await page.screenshot({ path: artifactPath('test-results/recovery-feedback-alert-dark.png') });
});
