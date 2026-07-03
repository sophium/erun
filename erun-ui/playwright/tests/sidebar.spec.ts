import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

test.describe('sidebar', () => {
  test('tenant toggle flips aria-expanded', async ({ app }) => {
    const before = await app.sidebar.isTenantExpanded(SEED_TENANT);
    await app.sidebar.toggleTenant(SEED_TENANT);
    const after = await app.sidebar.isTenantExpanded(SEED_TENANT);
    expect(after).toBe(!before);
    // Restore state so subsequent assertions in the suite don't drift.
    await app.sidebar.toggleTenant(SEED_TENANT);
  });

  test('opening an environment surfaces status feedback', async ({ app, page }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    // The opening status can surface in either the titlebar banner or a busy
    // overlay depending on busy state, so assert the text appears anywhere
    // rather than scoping to one surface.
    await expect(
      page.getByText(`Opening ${SEED_TENANT} / ${SEED_ENV_ALPHA}`, { exact: false }),
    ).toBeVisible({
      timeout: 15_000,
    });
  });
});
