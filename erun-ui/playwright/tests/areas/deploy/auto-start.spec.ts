import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

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

    // The field this predicate reads, asserted before the predicate is asked:
    // "not remote" has to mean the type was read and says local agent, not
    // that the control had not rendered yet.
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
    // `hasRemoteWorktree()` answers `false` for a local-agent env, and the
    // assertions above lean on that to mean "there is no Remote field to show".
    // While the dialog's config is still loading the body is a "Loading
    // config..." placeholder, so the Environment type control does not exist --
    // and folding that absence into `false` let the negative assertions be
    // reported without the field ever having been read. The read is held open
    // here rather than delayed, so the placeholder state is the only state the
    // test can observe.
    await page.route('**/__erun_invoke', async (route, request) => {
      const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (parsed.method === 'LoadEnvironmentConfig') {
        return; // never fulfils: the dialog stays in its loading body
      }
      await route.continue();
    });

    // The edit button's own onClick opens the dialog; openManageDialogFor
    // additionally waits for the loaded body, which is the state being ruled out.
    await app.sidebar.environmentRow(SEED_TENANT, SEED_ENV_ALPHA).dispatchEvent('click');
    await app.manageDialog.waitForOpen();

    await expect(app.manageDialog.environmentTypeSelect()).toHaveCount(0);
    await expect(app.manageDialog.hasRemoteWorktree()).rejects.toThrow(/not readable/);
  });

  test('first-time prompt stays closed when gate decides nothing would start', async ({ app }) => {
    // Error prevention (Nielsen #5): clicking an env must never pop the
    // first-time prompt when nothing would actually start.
    await expect(app.autoStartPromptDialog.locator()).toBeHidden();

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(app.autoStartPromptDialog.locator()).toBeHidden();
  });
});
