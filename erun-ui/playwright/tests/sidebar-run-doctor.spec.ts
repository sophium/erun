import { expect, test } from '../fixtures/erunApp.js';

// erun#1217: the sidebar's stethoscope dispatched into a guard that read the
// Manage dialog's own selection — which is only ever set together with the
// dialog opening — so clicking it with no dialog open silently did nothing.
// This spec locks the reachability fix: doctor now targets the sidebar's own
// selection, and is disabled (not silently inert) when nothing is selected.
//
// The CLI actually emitting the underlying `==> Doctor ...` trace lines is
// Go-side and cannot be observed in this harness: the inert `erun` stub exits
// immediately for any argument other than `open` and never emits them. See
// TestActivityTraceLineHandlerStartsAndFinishesDoctor,
// TestActivityTraceLineHandlerFinalizesDoctorOnFailure, and
// TestDoctorCompletedEventRecordsLastRunOutcome in
// erun-ui/activity_queue_app_test.go for that coverage. The frontend's own
// handling of the resulting `doctor-completed` Wails event — the piece that
// used to be wired to an unreachable terminal-exit handler — is reachable
// here by firing the event directly, which the third test below does.
test.describe('sidebar Run Doctor reachability (#1217)', () => {
  test('runs against the selected environment with no Manage dialog open', async ({
    app,
    page,
    seededEnv,
  }) => {
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);

    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible', timeout: 15_000 });
    const erunTab = page.getByRole('tab', { name: 'ERun', exact: true });
    await erunTab.waitFor({ state: 'visible', timeout: 15_000 });

    // Move off the Local tab first, so doctor switching back to it is an
    // observable effect rather than a coincidence of already being there.
    await erunTab.click();
    await expect(erunTab).toHaveAttribute('aria-selected', 'true');

    // The bug: this dispatched into a guard keyed off the (closed) Manage
    // dialog's own selection, which is never set outside that dialog.
    await expect(page.getByRole('dialog')).toHaveCount(0);

    const doctorButton = app.sidebar.runDoctorButton();
    await expect(doctorButton).toBeEnabled();
    await doctorButton.click();

    // `erun doctor` pipes into the shared Local shell (see
    // erun-ui/AGENTS.md § "Command Completion And State-Refresh Wiring"),
    // so the stable, observable effect of the dispatch actually firing is
    // the terminal switching back to Local.
    await expect(localTab).toHaveAttribute('aria-selected', 'true');
  });

  test('is disabled once no environment remains selected', async ({ app, seededEnv }) => {
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await expect(app.sidebar.runDoctorButton()).toBeEnabled();

    await app.sidebar.closeEnvironment(seededEnv.tenant, seededEnv.environment);

    const doctorButton = app.sidebar.runDoctorButton();
    await expect(doctorButton).toBeDisabled();
    await expect(doctorButton).toHaveAccessibleName('Run doctor');
  });

  // erun#1217: the result was never recorded — trackDoctorSession's only
  // reachable call site was unreachable (StartDoctorSession always returns
  // kind "local"), and its consumer was wired to the same terminal-exit
  // handler that never fires for a piped shared-shell command. The fix
  // replaces both with the `doctor-completed` Wails event; this drives that
  // event the way the CLI's `==> Doctor done` / `==> Doctor failed` trace
  // lines do (see handleDoctorTraceLine in erun-ui/activity_queue_app.go).
  test('records the last-run outcome the Manage dialog Access tab renders', async ({
    app,
    page,
    seededEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.tab('Access').click();

    const lastRun = page.getByRole('status').filter({ hasText: 'all checks passed' });
    const lastRunFailed = page.getByRole('alert').filter({ hasText: 'kubectl not reachable' });
    await expect(lastRun).toHaveCount(0);

    await page.evaluate(
      ({ tenant, environment }) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('doctor-completed', { tenant, environment, success: true });
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
        runtime.EventsEmit('doctor-completed', {
          tenant,
          environment,
          success: false,
          message: 'kubectl not reachable',
        });
      },
      { tenant: seededEnv.tenant, environment: seededEnv.environment },
    );
    await expect(lastRunFailed).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
