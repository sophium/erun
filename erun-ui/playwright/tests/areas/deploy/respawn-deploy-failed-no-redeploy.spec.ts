import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The harness cannot stage a failed deploy, so only the negative path is checked:
// the guard must not misfire on a healthy env.
test.describe('deploy-failed respawn guard', () => {
  test('healthy env opens normally and shows no deploy-failed marker', async ({ app, page }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    await app.tabStrip.waitForTab('Local');

    // AI/ERun tabs need a cluster the harness lacks.
    await expect(page.getByText(/deploy failed — not retrying automatically/i)).toBeHidden();
  });
});
