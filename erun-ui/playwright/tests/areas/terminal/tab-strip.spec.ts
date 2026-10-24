import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The respawn path needs a stopped managed cloud host and is Go-tested; here
// clicking an alive default tab must not flash "Reopening".

test.describe('terminal tab strip', () => {
  test('clicking an alive Local tab does not surface a Reopening overlay', async ({
    app,
    page,
  }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // Local spawns before ERun; wait without a cap shorter than the test budget.
    const localTab = app.tabStrip.tab('Local');
    await app.tabStrip.waitForTab('Local');
    await localTab.click();

    await expect(page.getByText(/Reopening Local shell/i)).toBeHidden();
    await expect(page.getByText(/Reopening AI session/i)).toBeHidden();
    await expect(page.getByText(/Reopening ERun session/i)).toBeHidden();
  });
});
