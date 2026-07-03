import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The AutoStart select and its first-time prompt only surface for a remote
// env bound to a managed cloud context with a stopped host — state the
// isolated harness cannot stage. These specs assert the reachable negative
// invariants (select stays hidden, prompt stays closed); persistence of the
// three AutoStart values is covered by the Go test
// TestSetEnvironmentAutoStartPersistsTriStateValue.

test.describe('auto-start gate', () => {
  test('Runtime-tab AutoStart select visibility tracks Remote field', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    expect(await app.manageDialog.hasRemoteWorktree()).toBe(false);

    await app.manageDialog.selectTab('Runtime');
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('Runtime');
    await expect(app.manageDialog.autoStartSelect()).toBeHidden();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('first-time prompt stays closed when gate decides nothing would start', async ({ app }) => {
    // Error prevention (Nielsen #5): clicking an env must never pop the
    // first-time prompt when nothing would actually start.
    await expect(app.autoStartPromptDialog.locator()).toBeHidden();

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(app.autoStartPromptDialog.locator()).toBeHidden();
  });
});
