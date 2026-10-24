import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The respawn path needs a real pod and is covered in Go; this pins that
// re-clicking an env with alive tabs neither duplicates tabs nor shows "Reopening".
test.describe('sidebar env re-click with alive default tabs', () => {
  test('does not duplicate Local/ERun/AI tabs or flash a Reopening overlay', async ({
    app,
    page,
  }) => {
    const tenant = SEED_TENANT;
    const env = SEED_ENV_ALPHA;

    await app.sidebar.openEnvironment(tenant, env);

    // Local spawns before ERun's StartSession, so it signals the open has settled.
    await app.tabStrip.waitForTab('Local');

    const initialLocalCount = await page.getByRole('tab', { name: 'Local', exact: true }).count();
    const initialERunCount = await page.getByRole('tab', { name: 'ERun', exact: true }).count();
    const initialAICount = await page.getByRole('tab', { name: 'AI', exact: true }).count();

    await app.sidebar.openEnvironment(tenant, env);

    await expect(page.getByText(/Reopening Local shell/i)).toBeHidden();
    await expect(page.getByText(/Reopening AI session/i)).toBeHidden();
    await expect(page.getByText(/Reopening ERun session/i)).toBeHidden();

    await expect(page.getByRole('tab', { name: 'Local', exact: true })).toHaveCount(
      initialLocalCount,
    );
    await expect(page.getByRole('tab', { name: 'ERun', exact: true })).toHaveCount(
      initialERunCount,
    );
    await expect(page.getByRole('tab', { name: 'AI', exact: true })).toHaveCount(initialAICount);
  });
});
