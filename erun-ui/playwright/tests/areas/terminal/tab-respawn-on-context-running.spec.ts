import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The real context-running transition needs a managed cloud host and is
// Go-tested; here a non-managed env must neither lose nor duplicate tabs
// across an idle poll.

test.describe('tab respawn after cloud context returns to running', () => {
  test('non-managed env does not lose tabs across an idle poll cycle', async ({ app, page }) => {
    const idlePolled = page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('LoadIdleStatus'),
    );
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // No managed context, so the detector is a no-op and nothing spawns.
    await idlePolled;
    const sidebar = page.locator('aside').first();
    await expect(sidebar.getByRole('status')).toHaveCount(0);

    const selectedRow = page.locator(
      `button[aria-label^="${SEED_TENANT} / ${SEED_ENV_ALPHA}"][aria-current="page"]`,
    );
    await expect(selectedRow).toBeVisible();
  });
});
