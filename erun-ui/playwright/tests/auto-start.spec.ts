import { expect, test } from '../fixtures/erunApp.js';

// auto-start covers the desktop-only auto-start gate added in
// feature/331-idle-stop-and-autostart-gate.
//
// The Runtime-tab "Auto-start when opening" select lives in the Idle-stop
// card alongside Timeout, Working hours, and Idle SSH activity threshold,
// because all four govern the env's start/stop lifecycle. It round-trips
// through the existing LoadEnvironmentConfig / SaveEnvironmentConfig path.
// The spec opens the manage dialog for the first env and asserts the
// select's presence tracks the env's "Remote environment" readonly field
// (visible on the General tab), without saving — same approach as the
// other manage specs, to avoid mutating the developer's actual ~/.erun/
// config.
//
// AutoStartPromptDialog itself opens only when openSelection has to decide
// whether to start a stopped EC2 host. The headless backend reflects the
// developer's real config, so we cannot reliably arrange a remote env with
// a stopped cloud context per AGENTS.md. Instead the suite asserts the
// negative invariant: clicking an env never surfaces the prompt when the
// gate decides the click would not start EC2. Persistence of the three
// AutoStart values is covered by Go unit tests
// (TestSetEnvironmentAutoStartPersistsTriStateValue) and the dialog itself
// mirrors ReconnectDialog's primitives.

test.describe('auto-start gate', () => {
  test('Runtime-tab AutoStart select visibility tracks Remote field', async ({ app }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);
    const env = envs[0]!;

    await app.sidebar.openManageDialogFor(tenant, env);
    await app.manageDialog.waitForOpen();

    // Environment type field is on the General tab (default landing tab).
    // Read it first, then hop to Runtime where the AutoStart select lives.
    const expectVisible = await app.manageDialog.hasRemoteWorktree();

    await app.manageDialog.selectTab('Runtime');
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('Runtime');
    expect(await app.manageDialog.autoStartSelectVisible()).toBe(expectVisible);

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

    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);

    await app.sidebar.openEnvironment(tenant, envs[0]!);
    // Give the gate's LoadEnvironmentConfig round-trip a moment to land,
    // then assert the prompt did not open. Using toBeHidden() (auto-retry)
    // rather than waitForTimeout keeps the test deterministic on slow
    // machines while still failing fast when the gate misfires.
    await expect(app.autoStartPromptDialog.locator()).toBeHidden({ timeout: 2_000 });
  });
});
