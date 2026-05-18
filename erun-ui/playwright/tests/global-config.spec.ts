import { test, expect } from '../fixtures/erunApp.js';

test.describe('global config dialog', () => {
  test('opens and closes cleanly', async ({ app }) => {
    await app.sidebar.openSettings();
    await app.globalConfigDialog.waitForOpen();
    await expect(app.globalConfigDialog.locator()).toBeVisible();

    const tenant = (await app.globalConfigDialog.getDefaultTenant()).trim();
    expect(tenant.length).toBeGreaterThan(0);

    await app.globalConfigDialog.cancel();
    await app.globalConfigDialog.waitForClosed();
  });
});
