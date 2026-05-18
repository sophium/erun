import { test, expect } from '../fixtures/erunApp.js';

test.describe('boot', () => {
  test('app loads and renders chrome', async ({ app, page }) => {
    await expect(page).toHaveTitle('ERun');
    await expect(page.getByText('Environments', { exact: true })).toBeVisible();
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThanOrEqual(1);
  });
});
