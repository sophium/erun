import { test, expect } from '../fixtures/erunApp.js';
import type { ManageTab } from '../pages/index.js';

test.describe('manage dialog', () => {
  test('iterates tabs and cancels', async ({ app }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);
    const env = envs[0]!;

    await app.sidebar.openManageDialogFor(tenant, env);
    await app.manageDialog.waitForOpen();

    const tabs: ManageTab[] = ['General', 'Runtime', 'AI', 'Ports', 'SSH'];
    for (const tab of tabs) {
      await app.manageDialog.selectTab(tab);
      await expect.poll(() => app.manageDialog.getActiveTab()).toBe(tab);
    }

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
