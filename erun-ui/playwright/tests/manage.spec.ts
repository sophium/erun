import { test, expect } from '../fixtures/erunApp.js';
import type { ManageTab } from '../pages/index.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

test.describe('manage dialog', () => {
  test('iterates tabs and cancels', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
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
