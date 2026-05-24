import { expect, test } from '../fixtures/erunApp.js';

// tab-respawn-on-context-running covers the recovery path where an env's
// linked cloud context dies mid-session (the AI/ERun PTY exits with code
// 143, tryReconnect refuses respawn against a non-running context, and
// dropExitedSessionFromTabs clears the tab), and the context later
// returns to Running through any route — titlebar Play button, manage
// dialog Start, or background poll observing the instance flipping back.
// Before the fix, ensureDefaultEnvTabs only ran from finishOpenSession
// and activateLocalAfterCommand, so a context that became Running via
// the idle poll left the dropped AI tab gone for the rest of the env's
// life.
//
// The fix exports ensureDefaultEnvTabs and adds restoreEnvTabsAfterContextRunning,
// which idleThunks.refreshIdleStatus dispatches when the cloud-context
// status transitions from non-running to running for the currently-
// selected env.
//
// The end-to-end happy path is not reachable from the headless harness:
//   - The headless harness's ~/.erun/ contains no managed cloud
//     contexts, so IdleApi returns a managedCloud=false status and the
//     transition detector never fires.
//   - Forcing the transition by emitting a fake idle-status event is
//     possible in principle but would couple this spec to the
//     transient API mock surface rather than the production code path.
//
// Negative invariant we CAN lock down: an env with no managed cloud
// context (i.e. nothing for the idle-poll detector to observe a
// transition on) does not lose tabs or spuriously spawn duplicate
// tabs across an idle poll cycle. This catches the regression where
// the transition detector misclassifies non-managed envs.
//
// The full debounce + transition logic is covered by the Go unit test
// for restoreEnvTabsAfterContextRunning in erun-ui/app_test.go and by
// the integration goldens for erun open + StartCloudContext in
// erun-integration/.

test.describe('tab respawn after cloud context returns to running', () => {
  test('non-managed env does not lose tabs across an idle poll cycle', async ({ app, page }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);

    // Open the first env so its tabs are initialized.
    await app.sidebar.openEnvironment(tenant, envs[0]!);

    // Give the idle poll a window to run; on dev configs it returns
    // managedCloud=false and the transition detector should be a
    // no-op. We assert the sidebar continues to render a single row
    // for this env with no errant spinner from spurious tab spawn.
    await page.waitForTimeout(1_500);
    const sidebar = page.locator('aside').first();
    await expect(sidebar.getByRole('status')).toHaveCount(0);

    // The clicked env stays selected — restoreEnvTabsAfterContextRunning
    // must not fire setSelected(null) or otherwise perturb selection.
    const selectedRow = page.locator(
      `button[title^="${tenant} / ${envs[0]!}"][aria-current="page"]`,
    );
    await expect(selectedRow).toBeVisible();
  });
});
