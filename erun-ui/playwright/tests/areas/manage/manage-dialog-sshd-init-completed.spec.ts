import { expect, test } from '../../../fixtures/erunApp.js';

// erun#1276: sshd-init's hidden-session tracking had the same structural
// defect erun#1268 fixed for Run Doctor — `StartSSHDInitSession` always
// returns kind "local" (it pipes `erun sshd init` into the shared Local
// shell), so the tracking call keyed on a dedicated-session exit was
// unreachable, and the outcome was never recorded anywhere. The fix follows
// the same pattern: `erun sshd init` now emits `==> SSHD init ...` /
// `==> SSHD init done` / `==> SSHD init failed` trace lines, and a new
// `sshd-init-completed` Wails event carries the outcome to the Manage
// dialog's SSH access section.
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
    await expect(lastRunFailed).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
