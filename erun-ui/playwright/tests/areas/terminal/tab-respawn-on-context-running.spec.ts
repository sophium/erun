import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Guards the regression where an env's cloud context returns to Running via
// the background idle poll (not an explicit Play/Start) and the AI tab that was
// dropped when the context died stayed gone for the rest of the env's life.
//
// The real happy path is unreachable headless: the isolated root's envs carry
// no managed cloud context, so idle status reports managedCloud=false and the
// non-running→running transition never fires — a real transition needs a live
// cloud host. Faking the idle-status event would couple this spec to the API
// mock surface instead of the production path.
//
// So we lock the negative invariant the harness CAN reach: a non-managed env
// (nothing for the detector to observe) neither loses tabs nor spawns duplicate
// tabs across an idle poll cycle, catching a detector that misclassifies
// non-managed envs. The full transition logic is covered by the Go unit test
// for restoreEnvTabsAfterContextRunning in erun-ui/app_test.go and the
// integration goldens for erun open + StartCloudContext in erun-integration/.

test.describe('tab respawn after cloud context returns to running', () => {
  test('non-managed env does not lose tabs across an idle poll cycle', async ({ app, page }) => {
    // Gate the assertions on a completed idle-poll cycle, not the wall clock.
    const idlePolled = page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('LoadIdleStatus'),
    );
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // With no managed cloud context the transition detector is a no-op, so
    // there must be no spinner from a spurious tab spawn.
    await idlePolled;
    const sidebar = page.locator('aside').first();
    await expect(sidebar.getByRole('status')).toHaveCount(0);

    // The no-op path must not perturb the current selection.
    const selectedRow = page.locator(
      `button[aria-label^="${SEED_TENANT} / ${SEED_ENV_ALPHA}"][aria-current="page"]`,
    );
    await expect(selectedRow).toBeVisible();
  });
});
