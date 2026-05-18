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
});
