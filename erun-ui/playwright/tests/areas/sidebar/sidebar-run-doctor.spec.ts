import { expect, test } from '../../../fixtures/erunApp.js';

// The stethoscope targets the sidebar's selection, not the Manage dialog's,
// and is disabled when nothing is selected.
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

    // Move off Local so doctor switching back to it is observable.
    await erunTab.click();
    await expect(erunTab).toHaveAttribute('aria-selected', 'true');

    await expect(page.getByRole('dialog')).toHaveCount(0);

    const doctorButton = app.sidebar.runDoctorButton();
    await expect(doctorButton).toBeEnabled();
    await doctorButton.click();

    // `erun doctor` pipes into the shared Local shell, so dispatch shows as a
    // switch back to Local.
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

  // The row's opened state lags the selection until StartSession resolves; the
  // close must wait for its control rather than match zero dots and stop.
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
    await expect(app.sidebar.runDoctorButton()).toBeEnabled();

    await app.sidebar.closeEnvironment(tenant, environment);

    await expect(app.sidebar.envOpenDot(tenant, environment)).toHaveCount(0);
    await expect(app.sidebar.runDoctorButton()).toBeDisabled();
  });

  // The outcome comes from the `doctor-completed` event, since a piped shared-shell
  // command fires no terminal-exit handler.
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
