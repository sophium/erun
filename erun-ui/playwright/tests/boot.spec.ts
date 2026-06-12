import { test, expect } from '../fixtures/erunApp.js';
import { SEED_TENANT } from '../fixtures/seedRoot.js';

test.describe('boot', () => {
  test('app loads and renders chrome', async ({ app, page }) => {
    await expect(page).toHaveTitle('ERun');
    await expect(page.getByText('Environments', { exact: true })).toBeVisible();
    // The isolated root is seeded with the pw tenant (global-setup); boot
    // must surface it — a missing row means the backend did not load the
    // seeded config tree.
    const tenants = await app.sidebar.tenants();
    expect(tenants).toContain(SEED_TENANT);
  });
});
