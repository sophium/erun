import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

test.describe('manage dialog environment health check', () => {
  test('shows the effective registry and runs an explicit health check with a recovery action', async ({
    app,
  }) => {
    // No save — the seeded baseline stays untouched. The seeded env is a
    // local-agent env that is never deployed, so the health check surfaces the
    // not-deployed state (or, if the stubbed cluster reports it up, an all-clear).
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    const dialog = app.manageDialog.locator();

    // (a) Effective container registry is shown legibly, not a blank field.
    await expect(app.manageDialog.registryInput(0)).toHaveValue('registry.example/test');

    // (b) The health section is present, and the check is an explicit user action
    // (never implicit on open — Nielsen user control).
    await expect(dialog.getByText('Environment health', { exact: true })).toBeVisible();
    const checkButton = dialog.getByRole('button', { name: 'Check environment' });
    await expect(checkButton).toBeVisible();

    await checkButton.click();

    // After the out-of-pod round-trip a result renders (visibility of system
    // status): either a not-deployed issue with a named Deploy recovery, or an
    // all-clear. Both are valid; a result MUST appear.
    await expect(
      dialog
        .getByRole('button', { name: 'Deploy' })
        .or(dialog.getByText('All checks passed.', { exact: true })),
    ).toBeVisible({ timeout: 20_000 });

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
