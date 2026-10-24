import { expect, test } from '../../../fixtures/erunApp.js';

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

  // The shared backend keeps earlier specs' failed activities, so scope to this card.
  const card = drawer.locator('article').filter({ hasText: 'petios/rihards-develop' }).first();

  await card
    .getByText('==> Deploy failed after 4s', { exact: false })
    .waitFor({ state: 'visible' });
  await expect(card.getByText('==> Deploy failed after 4s', { exact: false })).toBeVisible();

  // "UPGRADE FAILED" appears only in the captured output, never the summary.
  await expect(card.getByText('UPGRADE FAILED', { exact: false })).toHaveCount(0);
  await card.getByRole('button', { name: 'Show output' }).click();
  await card.getByText('UPGRADE FAILED', { exact: false }).waitFor({ state: 'visible' });
  await expect(card.getByText('UPGRADE FAILED', { exact: false })).toBeVisible();

  // Headless clipboard reads are unavailable, so the "Copied" flip is the only signal.
  const copyButton = card.getByRole('button', { name: 'Copy failure report' });
  await expect(copyButton).toBeVisible();
  await copyButton.click();
  await card.getByRole('button', { name: 'Copied' }).waitFor({ state: 'visible' });
  await expect(card.getByRole('button', { name: 'Copied' })).toBeVisible();

  // Asserted, not clicked: they would run real deploy/helm flows.
  await expect(card.getByRole('button', { name: 'Run doctor' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Rebuild & redeploy' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Clear pending helm release' })).toBeVisible();
});
