import { expect, test } from '../../../fixtures/erunApp.js';

// The inert `erun` stub never emits the SSHD init trace lines (covered in Go), so
// the `sshd-init-completed` event is fired directly.
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
