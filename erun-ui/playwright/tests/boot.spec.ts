import { test, expect } from '../fixtures/erunApp.js';
import { SEED_TENANT } from '../fixtures/seedRoot.js';

test.describe('boot', () => {
  test('app loads and renders chrome', async ({ app, page }) => {
    await expect(page).toHaveTitle('ERun');
    await expect(page.getByText('Environments', { exact: true })).toBeVisible();
    // A missing row means the backend did not load the seeded config tree on boot.
    const tenants = await app.sidebar.tenants();
    expect(tenants).toContain(SEED_TENANT);
  });
});
