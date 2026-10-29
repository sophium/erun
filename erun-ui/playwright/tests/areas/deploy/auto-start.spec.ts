import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The AutoStart select needs a stopped managed cloud host the harness cannot stage,
// so these specs assert only the negative invariants.

test.describe('auto-start gate', () => {
  test('Runtime-tab AutoStart select visibility tracks Remote field', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    // Read the type first so "not remote" can't mean "not rendered yet".
    await expect(app.manageDialog.environmentTypeSelect()).toContainText(/local agent/i);
    expect(await app.manageDialog.hasRemoteWorktree()).toBe(false);

    await app.manageDialog.selectTab('Runtime');
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('Runtime');
    await expect(app.manageDialog.autoStartSelect()).toBeHidden();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('the env-type read refuses to answer while the manage body is still loading', async ({
    app,
    page,
  }) => {
    // Hold the config read open so the "Loading config..." placeholder is the only state.
    await page.route('**/__erun_invoke', async (route, request) => {
      const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (parsed.method === 'LoadEnvironmentConfig') {
        return; // never fulfils: the dialog stays in its loading body
      }
      await route.continue();
    });

    // Bypasses openManageDialogFor, which waits for the loaded body this case rules out.
    await app.sidebar.environmentRow(SEED_TENANT, SEED_ENV_ALPHA).dispatchEvent('click');
    await app.manageDialog.waitForOpen();

    await expect(app.manageDialog.environmentTypeSelect()).toHaveCount(0);
    await expect(app.manageDialog.hasRemoteWorktree()).rejects.toThrow(/not readable/);
  });

  test('first-time prompt stays closed when gate decides nothing would start', async ({ app }) => {
    await expect(app.autoStartPromptDialog.locator()).toBeHidden();

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(app.autoStartPromptDialog.locator()).toBeHidden();
  });
});
