import { expect, test } from '../../../fixtures/erunApp.js';

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

    const localTab = app.tabStrip.tab('Local');
    await app.tabStrip.waitForTab('Local');
    const erunTab = app.tabStrip.tab('ERun');
    await app.tabStrip.waitForTab('ERun');

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

  // The contended red on the case above, forced on demand instead of waited
  // for.
  //
  // `selection.selected` -- what the line above asserts on -- is set at the top
  // of openSelection's thunk, while the row's own "opened here" state reads the
  // desktop's tabs for the env, which only exist once that thunk's StartSession
  // has resolved. A close issued in between used to find no close control,
  // match zero dots, and declare itself done without pressing anything, so the
  // env stayed open and the disabled assertion watched a state that could never
  // arrive; under contention that window is wide enough to lose, which is how
  // this spec reddened a full-suite gate (the Run doctor button polled enabled
  // for the whole 10s the assertion waited).
  //
  // Holding the session open widens that window from a few milliseconds to a
  // named, deterministic one. The close must still close: it converges on the
  // control appearing rather than accepting the row's silence as a finished
  // close.
  test('a close issued before the row reports the env opened still closes it', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await page.route('**/__erun_invoke', async (route, request) => {
      const method = (JSON.parse(request.postData() ?? '{}') as { method?: string }).method ?? '';
      if (/^Start(Local)?Session$/.test(method)) {
        await new Promise<void>((resolve) => setTimeout(resolve, 12_000));
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(tenant, environment);
    // The only precondition the line it reproduces has: the selection is set,
    // so the caller has every reason to believe the env is open.
    await expect(app.sidebar.runDoctorButton()).toBeEnabled();

    await app.sidebar.closeEnvironment(tenant, environment);

    await expect(app.sidebar.envOpenDot(tenant, environment)).toHaveCount(0);
    await expect(app.sidebar.runDoctorButton()).toBeDisabled();
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
