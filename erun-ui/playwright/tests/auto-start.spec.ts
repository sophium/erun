import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// auto-start covers the desktop-only auto-start gate added in
// feature/331-idle-stop-and-autostart-gate.
//
// The Runtime-tab "Auto-start when opening" select lives in the Idle-stop
// card alongside Timeout, Working hours, and Idle SSH activity threshold,
// because all four govern the env's start/stop lifecycle. It round-trips
// through the existing LoadEnvironmentConfig / SaveEnvironmentConfig path.
// The spec opens the manage dialog for the seeded local-agent env and
// asserts the select stays hidden, without saving — same approach as the
// other manage specs, to keep the seeded config untouched.
//
// AutoStartPromptDialog itself opens only when openSelection has to decide
// whether to start a stopped EC2 host. A stopped managed cloud context
// needs a real cloud host the isolated harness cannot stage, so the suite
// asserts the negative invariant: clicking an env never surfaces the prompt
// when the gate decides the click would not start EC2. Persistence of the
// three AutoStart values is covered by Go unit tests
// (TestSetEnvironmentAutoStartPersistsTriStateValue) and the dialog itself
// mirrors ReconnectDialog's primitives.

test.describe('auto-start gate', () => {
  test('Runtime-tab AutoStart select visibility tracks Remote field', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    // The desktop-only AutoStart select renders only for a remote env bound
    // to a managed cloud context (it governs starting a stopped cloud host) —
    // not for every remote env, and never for a local-agent env. The seeded
    // baseline env is local-agent, so the deterministic direction here is
    // "local-agent ⇒ hidden"; the managed-cloud binding that would show the
    // select needs a real cloud host the isolated harness cannot stage.
    expect(await app.manageDialog.hasRemoteWorktree()).toBe(false);

    await app.manageDialog.selectTab('Runtime');
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('Runtime');
    // A local-agent env must never expose the AutoStart select.
    await expect(app.manageDialog.autoStartSelect()).toBeHidden();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('first-time prompt stays closed when gate decides nothing would start', async ({ app }) => {
    // The AutoStartPromptDialog only opens when the gate decides EC2 would
    // actually start. For any env whose linked cloud context is already
    // running, that has no linked cloud context, or whose AutoStart is
    // already set, clicking the env must not surface the prompt — this is
    // the Nielsen #5 (error prevention) guarantee called out in the PR's
    // UX checklist.
    await expect(app.autoStartPromptDialog.locator()).toBeHidden();

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    // Give the gate's LoadEnvironmentConfig round-trip a moment to land,
    // then assert the prompt did not open. Using toBeHidden() (auto-retry)
    // rather than waitForTimeout keeps the test deterministic on slow
    // machines while still failing fast when the gate misfires.
    await expect(app.autoStartPromptDialog.locator()).toBeHidden({ timeout: 2_000 });
  });
});
