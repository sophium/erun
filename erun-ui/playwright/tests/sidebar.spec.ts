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
    // Opening an env dispatches showTerminalMessage(..., busy=true). When
    // busy+message coexist the titlebar status banner is suppressed in
    // favour of a TerminalBusyOverlay rendered over the terminal pane.
    // Either surface is acceptable — assert the "Opening <tenant> /
    // <env>" string appears anywhere on the page.
    await expect(
      page.getByText(`Opening ${SEED_TENANT} / ${SEED_ENV_ALPHA}`, { exact: false }),
    ).toBeVisible({
      timeout: 15_000,
    });
  });
});
