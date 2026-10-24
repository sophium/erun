import { expect, test } from '../../../fixtures/erunApp.js';

// `StartSSHDInitSession` always returns kind "local" (it pipes
// `erun sshd init` into the shared Local shell), so there is no dedicated
// session exit to track, as with Run Doctor. Instead `erun sshd init` emits
// `==> SSHD init ...` / `==> SSHD init done` / `==> SSHD init failed` trace
// lines, and the `sshd-init-completed` Wails event carries the outcome to the
// Manage dialog's SSH access section.
//
// The CLI actually emitting those trace lines is Go-side and cannot be
// observed in this harness: the inert `erun` stub exits immediately for any
// argument other than `open` and never emits them. See
// TestActivityTraceLineHandlerStartsAndFinishesSSHDInit,
// TestActivityTraceLineHandlerFinalizesSSHDInitOnFailure, and
// TestSSHDInitCompletedEventRecordsLastRunOutcome in
// erun-ui/activity_queue_app_test.go for that coverage. The frontend's own
// handling of the resulting `sshd-init-completed` event is reachable here by
// firing it directly, the way those trace lines drive it in production.
test.describe('SSH access last-run outcome (#1276)', () => {
  test('records the last-run outcome the Manage dialog SSH access section renders', async ({
    app,
    page,
    seededEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.tab('Access').click();

    const lastRun = page.getByRole('status').filter({ hasText: 'SSHD enabled' });
    const lastRunFailed = page.getByRole('alert').filter({ hasText: 'connection refused' });
    await expect(lastRun).toHaveCount(0);
    await expect(lastRunFailed).toHaveCount(0);

    await page.evaluate(
      ({ tenant, environment }) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('sshd-init-completed', { tenant, environment, success: true });
      },
      { tenant: seededEnv.tenant, environment: seededEnv.environment },
    );
    // Converge on the render the event triggers before asserting on it,
    // rather than racing that render against a bare expect's fixed timeout.
    await lastRun.waitFor({ state: 'visible' });
    await expect(lastRun).toBeVisible();

    await page.evaluate(
      ({ tenant, environment }) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('sshd-init-completed', {
          tenant,
          environment,
          success: false,
          message: 'connection refused',
        });
      },
      { tenant: seededEnv.tenant, environment: seededEnv.environment },
    );
    await lastRunFailed.waitFor({ state: 'visible' });
    await expect(lastRunFailed).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
