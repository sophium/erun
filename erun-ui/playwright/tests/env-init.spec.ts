import { test, expect } from '../fixtures/erunApp.js';

test.describe('environment init dialog', () => {
  test('opens with tenant pre-populated and cancels', async ({ app }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();
    await expect(app.envInitDialog.locator()).toBeVisible();

    // When at least one tenant exists, the dialog pre-populates the
    // tenant field with the current selection's tenant; assert the field
    // is present and non-empty.
    const tenantInput = app.envInitDialog.tenantInput();
    await expect(tenantInput).toBeVisible();
    const tenant = (await tenantInput.inputValue()).trim();
    expect(tenant.length).toBeGreaterThan(0);

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('init mode shows the "create" description and a submit-reason status line', async ({
    app,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // The new mode-aware description text (item 8 in the UX plan) must
    // mention "Create" rather than the generic "Enter the tenant and
    // environment name." copy. Match a regex so both the "with
    // pre-populated values" and "blank" copy branches pass.
    const dialog = app.envInitDialog.locator();
    await expect(dialog.getByText(/Create|create/).first()).toBeVisible();

    // The submit-disabled reason container exists with role=status; it
    // may be empty when the dialog has no current blockers. The
    // important invariant is that the live-region container is in the
    // DOM (item 11) so blocking reasons can surface there without a
    // re-render shift.
    const reason = app.page.locator('#environment-dialog-submit-reason');
    await expect(reason).toHaveCount(1);

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });
});
