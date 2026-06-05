import { expect, test } from '../fixtures/erunApp.js';

// app-notification covers the new Go-side `app-notification` Wails
// event that surfaces one-shot info/success events as transient,
// auto-dismissing toasts. The canonical caller is the idle auto-stop
// success line in erun-ui/idle_status.go: before issue #361 it rode on
// the persistent `app-status` channel, which latched the message into
// the titlebar pill long after the cloud context had been restarted
// elsewhere — see the issue body for the user-facing symptom.
//
// The Go side is covered by erun-ui/notifications_test.go. This spec
// drives the same React path the production event ends up using: emit
// the Wails event directly and verify the titlebar pill renders the
// notification with the matching kind, then auto-dismisses after the
// 3.2 s timer in notificationThunks.ts.

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
    await expect(pill).toBeVisible({ timeout: 5_000 });

    // notificationThunks.ts auto-dismisses success/info after 3.2 s.
    // Give the timer 5 s headroom so we are not racing with it; if the
    // pill is still up at that point the auto-dismiss broke.
    await expect(pill).toHaveCount(0, { timeout: 5_000 });
  });

  test('error notification persists (no auto-dismiss)', async ({ app: _app, page }) => {
    const message = 'Backend pinned a problem you should read.';
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
    await expect(pill).toBeVisible({ timeout: 5_000 });

    // Confirm the toast does NOT auto-dismiss on the success/info
    // timer. 4 s easily clears the 3.2 s window without making this
    // spec sluggish.
    await page.waitForTimeout(4_000);
    await expect(pill).toBeVisible();
  });

  test('payload with empty message is ignored', async ({ app: _app, page }) => {
    // An empty payload must add no notification toast. Compare the
    // role=status / role=alert counts before and after rather than
    // asserting an absolute zero: the titlebar idle-status widget also
    // carries role=status whenever an env is active (true on a populated
    // ~/.erun), so a global count of 0 is not a safe invariant. A no-op
    // dispatch must leave the counts unchanged.
    const statusBefore = await page.locator('[role="status"]').count();
    const alertBefore = await page.locator('[role="alert"]').count();
    await page.evaluate(() => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('app-notification', { kind: 'info', message: '   ' });
    });
    // Wait a short tick to give the dispatcher a chance to no-op.
    await page.waitForTimeout(200);
    await expect(page.locator('[role="status"]')).toHaveCount(statusBefore);
    await expect(page.locator('[role="alert"]')).toHaveCount(alertBefore);
  });
});
