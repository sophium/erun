import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Seeded envs have no managed cloud context, so only the negative mount gate is
// testable here; the populated tooltip is covered by Go unit and integration tests.

test.describe('idle tooltip', () => {
  test('idle widget stays unmounted for an env without a managed cloud context', async ({
    app,
    page,
  }) => {
    // Wait on the poll RPC: toBeHidden alone would pass before any poll ran.
    const idlePolled = page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('LoadIdleStatus'),
    );
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    await idlePolled;
    const badge = app.titlebar.idleStatusBadge();
    await expect(badge).toBeHidden();
  });
});
