import { test, expect } from '../fixtures/erunApp.js';

test.describe('sidebar', () => {
  test('tenant toggle flips aria-expanded', async ({ app }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const before = await app.sidebar.isTenantExpanded(tenant);
    await app.sidebar.toggleTenant(tenant);
    const after = await app.sidebar.isTenantExpanded(tenant);
    expect(after).toBe(!before);
    // Restore state so subsequent assertions in the suite don't drift.
    await app.sidebar.toggleTenant(tenant);
  });

  test('opening an environment surfaces status feedback', async ({ app, page }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);
    const env = envs[0]!;

    await app.sidebar.openEnvironment(tenant, env);
    // Opening an env dispatches showTerminalMessage(..., busy=true). When
    // busy+message coexist the titlebar status banner is suppressed in
    // favour of a TerminalBusyOverlay rendered over the terminal pane.
    // Either surface is acceptable — assert the "Opening <tenant> /
    // <env>" string appears anywhere on the page.
    await expect(page.getByText(`Opening ${tenant} / ${env}`, { exact: false })).toBeVisible({
      timeout: 15_000,
    });
  });
});
