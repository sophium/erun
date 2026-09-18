import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { test, expect } from '../../../fixtures/erunApp.js';

// The repository path is editable for local-agent envs so an operator can retarget
// a moved repo in place instead of hand-editing config; non-local-agent envs have
// no local host path, so it stays read-only.
test.describe('manage dialog repository path (#709)', () => {
  test('edits and persists the repository path for a local-agent env', async ({
    app,
    seededEnv,
  }) => {
    // The dialog validates this field with init's own definition — a local-agent
    // env hostPath-mounts the path into its pod — so the retarget target has to
    // be a real absolute directory on this machine. A save of a path that is not
    // is refused with init's reason, which correctly leaves the tab dirty; the
    // value this spec persists is therefore staged rather than invented.
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
