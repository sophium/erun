import { expect, test } from '../fixtures/erunApp.js';

// ux-tooltip-rules covers AGENTS.md L104 — "Native `title` is only
// acceptable for non-essential truncation hints" — for the controls that
// previously used native title= for meaningful product info. The fix
// either:
//   - drops `title=` entirely and lets the new aria-label carry the
//     accessible name (sidebar rows), or
//   - wraps the trigger in the app Tooltip primitive (activity drawer
//     clear-all button, activity-card dismiss button).
//
// The spec asserts the negative invariant the headless harness CAN
// reach: the targeted buttons no longer set the HTML `title` attribute.
// Hover-driven Tooltip visibility for individual cards depends on
// real backend state the harness does not stage; the negative invariant
// is the part most likely to regress mechanically (someone reaches for
// `title=` again because it is shorter than wrapping in Tooltip).

test.describe('UX tooltip rules', () => {
  test('sidebar env rows carry aria-label and no title attribute', async ({ app }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);
    const env = envs[0]!;

    const rowLocator = app.page.locator(`button[aria-label^="${tenant} / ${env}"]`).first();
    await expect(rowLocator).toBeVisible();
    await expect(rowLocator).not.toHaveAttribute('title', /.*/);
    await expect(rowLocator).toHaveAttribute(
      'aria-label',
      new RegExp(`^${escapeRegExp(`${tenant} / ${env}`)}`),
    );
  });

  test('sidebar tenant rows carry aria-label and no title attribute', async ({ app }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;

    const selectButton = app.page.getByRole('button', { name: `Open ${tenant} dashboard` }).first();
    await expect(selectButton).toBeVisible();
    await expect(selectButton).not.toHaveAttribute('title', /.*/);
  });

  test('activity drawer clear-all button uses tooltip primitive', async ({ app, page }) => {
    // Stage two running activities through the same Wails-event path the
    // real activity poller uses, then open the drawer. With >1 entry the
    // "Force dismiss all" clear-all button surfaces in the Now section.
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
    await expect(clearAll).toBeVisible({ timeout: 5_000 });
    // The new ClearAllButton drops the native title attribute and routes
    // the hint through Tooltip + aria-label, so the title is absent and
    // aria-label carries the explanatory hint.
    await expect(clearAll).not.toHaveAttribute('title', /.*/);
    await expect(clearAll).toHaveAttribute('aria-label', /Force dismiss all/);
    await expect(clearAll).toHaveAttribute('aria-label', /Underlying processes are not killed/);
  });

  test('activity card dismiss button uses tooltip primitive', async ({ app, page }) => {
    // Seed one finished entry so the dismiss button surfaces in Recent.
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

    // The dismiss button carries the same aria-label as the previous
    // implementation (Dismiss for finished entries); verify the redundant
    // native title is gone.
    const dismissButton = app.activityDrawer
      .locator()
      .getByRole('button', { name: 'Dismiss', exact: true })
      .first();
    await expect(dismissButton).toBeVisible({ timeout: 5_000 });
    await expect(dismissButton).not.toHaveAttribute('title', /.*/);
  });
});

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
