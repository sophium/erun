import { test, expect } from '../fixtures/erunApp.js';
import type { ManageTab } from '../pages/index.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

test.describe('manage dialog', () => {
  test('iterates tabs and cancels', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    const tabs: ManageTab[] = ['General', 'Runtime', 'AI', 'Ports', 'Access'];
    for (const tab of tabs) {
      await app.manageDialog.selectTab(tab);
      await expect.poll(() => app.manageDialog.getActiveTab()).toBe(tab);
    }

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The Ports tab's Service column names the user-facing purpose of each
  // port, not the internal protocol/service key (#1218).
  test('Ports tab labels services by purpose, not internal name', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Ports');

    const dialog = app.manageDialog.locator();
    await expect(dialog.getByText('AI agent connection', { exact: true })).toBeVisible();
    await expect(dialog.getByText('Environment API', { exact: true })).toBeVisible();
    await expect(dialog.getByText('Contribute app preview', { exact: true })).toBeVisible();
    await expect(dialog.getByText('mcp', { exact: true })).toHaveCount(0);
    await expect(dialog.getByText('contribute-app', { exact: true })).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // Workspace sync is a distinct concept from SSH shell access (#1218): it
  // gets its own titled section on the Access tab instead of hiding as an
  // unlabeled checkbox nested inside "SSH access".
  test('Access tab separates SSH access from workspace sync', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('Access');

    const dialog = app.manageDialog.locator();
    await expect(dialog.getByText('SSH access', { exact: true })).toBeVisible();
    await expect(dialog.getByText('Workspace sync', { exact: true })).toBeVisible();

    const syncCheckbox = dialog.getByRole('checkbox', { name: 'Enable workspace sync' });
    await expect(syncCheckbox).toBeVisible();
    // The seeded env has SSHD disabled, so workspace sync stays disabled and
    // explains why rather than leaving the user to guess.
    await expect(syncCheckbox).toBeDisabled();
    await expect(dialog.getByText('Requires SSH access to be enabled.')).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
