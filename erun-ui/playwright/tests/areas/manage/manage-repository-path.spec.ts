import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { test, expect } from '../../../fixtures/erunApp.js';

test.describe('manage dialog repository path (#709)', () => {
  test('edits and persists the repository path for a local-agent env', async ({
    app,
    seededEnv,
  }) => {
    // Validated like init: the path must be a real absolute directory, so it is staged.
    const newRepoPath = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-pw-repo-'));
    try {
      await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
      await app.manageDialog.waitForOpen();

      await expect(app.manageDialog.repositoryPathInput()).toBeVisible();
      await expect(app.manageDialog.repositoryPathInput()).toBeEnabled();
      expect(await app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

      await app.manageDialog.setRepositoryPath(newRepoPath);
      await expect.poll(() => app.manageDialog.repositoryPathValue()).toBe(newRepoPath);
      await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(true);

      await app.manageDialog.save();
      await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

      await app.manageDialog.cancel();
      await app.manageDialog.waitForClosed();
      await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
      await app.manageDialog.waitForOpen();
      await expect.poll(() => app.manageDialog.repositoryPathValue()).toBe(newRepoPath);

      await app.manageDialog.cancel();
      await app.manageDialog.waitForClosed();
    } finally {
      fs.rmSync(newRepoPath, { recursive: true, force: true });
    }
  });

  test('keeps the repository path read-only for a non-local-agent env', async ({
    app,
    seededEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.chooseEnvironmentType('Runtime (no worktree; receives deploys)');
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

    await expect(app.manageDialog.repositoryPathInput()).toHaveCount(0);
    await expect(app.manageDialog.repositoryPathReadonlyValue()).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
