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
    await expect(pill).toBeVisible();

    // notificationThunks.ts auto-dismisses success/info after 3.2 s.
    // Give the timer 5 s headroom so we are not racing with it; if the
    // pill is still up at that point the auto-dismiss broke.
    await expect(pill).toHaveCount(0);
  });

  test('error notification persists (no auto-dismiss)', async ({ app: _app, page }) => {
    const message = 'Backend pinned a problem you should read.';
    // Take over the page clock so the 3.2 s success/info auto-dismiss timer can
    // be advanced deterministically instead of slept through. The notification
    // slot is single-occupancy (notificationThunks.showNotification replaces the
    // one toast), so a sibling info toast would race the error rather than time
    // alongside it — the clock is the reliable signal that the window elapsed.
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

    // Advance well past the 3.2 s window that auto-dismisses success/info
    // toasts. An error toast sets no timer, so it must still be visible.
    await page.clock.fastForward(5_000);
    await expect(pill).toBeVisible();
  });

  test('payload with empty message is ignored', async ({ app: _app, page }) => {
    // An empty payload must add no notification toast. Compare the
    // role=status / role=alert counts before and after rather than
    // asserting an absolute zero: the titlebar idle-status widget also
    // carries role=status whenever an env with a managed cloud context is
    // active, so a global count of 0 is not a future-proof invariant. A
    // no-op dispatch must leave the counts unchanged.
    const statusBefore = await page.locator('[role="status"]').count();
    const alertBefore = await page.locator('[role="alert"]').count();
    const sentinel = 'Sentinel error toast proving the empty payload was processed.';
    // Emit the empty payload, then a valid error sentinel. Events are ordered,
    // so once the sentinel renders the empty dispatch has provably been
    // processed — bounding the no-op window with a real event, not a sleep. The
    // sentinel is an error toast so it persists (no auto-dismiss race).
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
    // The empty payload added no toast: the status count is unchanged and only
    // the sentinel raised the alert count.
    await expect(page.locator('[role="status"]')).toHaveCount(statusBefore);
    await expect(page.locator('[role="alert"]')).toHaveCount(alertBefore + 1);
  });
});
