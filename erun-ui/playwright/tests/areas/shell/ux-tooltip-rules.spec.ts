import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Native `title` is only for non-essential truncation hints; these controls must not set it.

test.describe('UX tooltip rules', () => {
  test('sidebar env rows carry aria-label and no title attribute', async ({ app }) => {
    const tenant = SEED_TENANT;
    const env = SEED_ENV_ALPHA;

    const rowLocator = app.page.locator(`button[aria-label^="${tenant} / ${env}"]`).first();
    await expect(rowLocator).toBeVisible();
    await expect(rowLocator).not.toHaveAttribute('title', /.*/);
    await expect(rowLocator).toHaveAttribute(
      'aria-label',
      new RegExp(`^${escapeRegExp(`${tenant} / ${env}`)}`),
    );
  });

  test('sidebar tenant rows carry aria-label and no title attribute', async ({ app }) => {
    const selectButton = app.page
      .getByRole('button', { name: `Open ${SEED_TENANT} dashboard` })
      .first();
    await expect(selectButton).toBeVisible();
    await expect(selectButton).not.toHaveAttribute('title', /.*/);
  });

  test('activity drawer clear-all button uses tooltip primitive', async ({ app, page }) => {
    // Clear-all only surfaces with more than one entry.
    await page.evaluate(() => {
      const runtime = (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime;
      const baseTime = new Date().toISOString();
      runtime.EventsEmit('activity:state', {
        id: 'ux-spec-running-1',
        command: 'open',
        tenant: 'ux-spec',
        environment: 'env-a',
        status: 'running',
        startedAt: baseTime,
        lastUpdated: baseTime,
        source: 'action',
        actionKind: 'open',
        summary: 'open ux-spec/env-a',
      });
      runtime.EventsEmit('activity:state', {
        id: 'ux-spec-running-2',
        command: 'open',
        tenant: 'ux-spec',
        environment: 'env-b',
        status: 'running',
        startedAt: baseTime,
        lastUpdated: baseTime,
        source: 'action',
        actionKind: 'open',
        summary: 'open ux-spec/env-b',
      });
    });

    await app.activityDrawer.open();
    await expect(app.activityDrawer.locator()).toBeVisible();

    const clearAll = app.activityDrawer
      .locator()
      .getByRole('button', { name: /Force dismiss all/ });
    await clearAll.waitFor({ state: 'visible' });
    await expect(clearAll).toBeVisible();
    await expect(clearAll).not.toHaveAttribute('title', /.*/);
    await expect(clearAll).toHaveAttribute('aria-label', /Force dismiss all/);
    await expect(clearAll).toHaveAttribute('aria-label', /Underlying processes are not killed/);
  });

  test('activity card dismiss button uses tooltip primitive', async ({ app, page }) => {
    await page.evaluate(() => {
      const ts = new Date().toISOString();
      (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime.EventsEmit('activity:state', {
        id: 'ux-spec-finished-1',
        command: 'open',
        tenant: 'ux-spec',
        environment: 'env-finished',
        status: 'succeeded',
        startedAt: ts,
        endedAt: ts,
        lastUpdated: ts,
        source: 'action',
        actionKind: 'open',
        summary: 'open ux-spec/env-finished',
      });
    });

    await app.activityDrawer.open();
    await expect(app.activityDrawer.locator()).toBeVisible();

    const dismissButton = app.activityDrawer
      .locator()
      .getByRole('button', { name: 'Dismiss', exact: true })
      .first();
    await dismissButton.waitFor({ state: 'visible' });
    await expect(dismissButton).toBeVisible();
    await expect(dismissButton).not.toHaveAttribute('title', /.*/);
  });
});

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
