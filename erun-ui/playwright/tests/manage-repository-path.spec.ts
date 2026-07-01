import { test, expect } from '../fixtures/erunApp.js';

// Issue #709 — the Manage dialog's General tab "Repository path" is an editable
// Input for local-agent envs (the host worktree path, EnvConfig.LocalRepoPath),
// so an operator can retarget a moved repo in place instead of hand-editing
// config.yaml. The edit lights the General tab's unsaved-changes dot and
// round-trips through the Go load/save path. For non-local-agent envs it stays a
// read-only field (their repo is not a local host path). Uses a per-test seeded
// local-agent env and a real save+reload (the manage-environment-type technique).
test.describe('manage dialog repository path (#709)', () => {
  test('edits and persists the repository path for a local-agent env', async ({
    app,
    seededEnv,
  }) => {
    const newRepoPath = `/tmp/erun-pw-repo-${String(Date.now())}`;
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    // Local-agent env: the field is an editable, enabled textbox — not a
    // read-only label.
    await expect(app.manageDialog.repositoryPathInput()).toBeVisible();
    await expect(app.manageDialog.repositoryPathInput()).toBeEnabled();
    expect(await app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

    // Editing the path updates the draft and lights the General tab's
    // unsaved-changes dot (a visible affordance for the edit).
    await app.manageDialog.setRepositoryPath(newRepoPath);
    await expect.poll(() => app.manageDialog.repositoryPathValue()).toBe(newRepoPath);
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(true);

    // Saving persists it; the dot clears.
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

    // The new path round-trips through the Go load/save path: reopening the
    // dialog shows it.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();
    await expect.poll(() => app.manageDialog.repositoryPathValue()).toBe(newRepoPath);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('keeps the repository path read-only for a non-local-agent env', async ({
    app,
    seededEnv,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    // Switching the type to runtime (no worktree) and saving flips the field to
    // read-only: a runtime env has no local host path to edit.
    await app.manageDialog.chooseEnvironmentType('Runtime (no worktree; receives deploys)');
    await app.manageDialog.save();
    await expect.poll(() => app.manageDialog.tabHasUnsavedChanges('General')).toBe(false);

    await expect(app.manageDialog.repositoryPathInput()).toHaveCount(0);
    await expect(app.manageDialog.repositoryPathReadonlyValue()).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
