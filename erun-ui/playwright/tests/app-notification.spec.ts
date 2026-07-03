import { expect, test } from '../fixtures/erunApp.js';

// The `app-notification` event surfaces one-shot info/success events as
// transient, auto-dismissing toasts. It exists because the idle auto-stop
// success previously rode the persistent `app-status` channel, which latched
// the message into the titlebar pill long after its cloud context had been
// restarted elsewhere.

test.describe('app-notification toast', () => {
  test('info notification renders in the titlebar then auto-dismisses', async ({
    app: _app,
    page,
  }) => {
    const message = 'Stopped idle cloud context cluster-cloud.';

    await page.evaluate(
      (payload) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('app-notification', payload);
      },
      { kind: 'info', message },
    );

    const pill = page.getByRole('status').filter({ hasText: message });
    await expect(pill).toBeVisible();

    await expect(pill).toHaveCount(0);
  });

  test('error notification persists (no auto-dismiss)', async ({ app: _app, page }) => {
    const message = 'Backend pinned a problem you should read.';
    // The notification slot is single-occupancy, so the error toast can't be
    // timed against a sibling info toast — advance the clock past the
    // auto-dismiss window instead of racing a second toast.
    await page.clock.install();
    await page.evaluate(
      (payload) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('app-notification', payload);
      },
      { kind: 'error', message },
    );

    const pill = page.getByRole('alert').filter({ hasText: message });
    await expect(pill).toBeVisible();

    await page.clock.fastForward(5_000);
    await expect(pill).toBeVisible();
  });

  test('payload with empty message is ignored', async ({ app: _app, page }) => {
    // The titlebar idle-status widget also carries role=status when an env with
    // a managed cloud context is active, so an ignored payload can't be checked
    // against an absolute count of zero — compare before/after instead.
    const statusBefore = await page.locator('[role="status"]').count();
    const alertBefore = await page.locator('[role="alert"]').count();
    const sentinel = 'Sentinel error toast proving the empty payload was processed.';
    // Events are ordered, so once this error sentinel renders the empty dispatch
    // has provably been processed — a real event bounds the "nothing happened"
    // assertion instead of a sleep. Error kind so the sentinel never
    // auto-dismisses mid-assertion.
    await page.evaluate((msg) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('app-notification', { kind: 'info', message: '   ' });
      runtime.EventsEmit('app-notification', { kind: 'error', message: msg });
    }, sentinel);
    await expect(page.getByRole('alert').filter({ hasText: sentinel })).toBeVisible();
    await expect(page.locator('[role="status"]')).toHaveCount(statusBefore);
    await expect(page.locator('[role="alert"]')).toHaveCount(alertBefore + 1);
  });
});
