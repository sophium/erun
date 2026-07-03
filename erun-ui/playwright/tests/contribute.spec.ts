import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The positive click path fires a real contribute_clone MCP call inside the
// env, which the headless harness cannot stage, so this spec only locks the
// visibility gating (eligible = a non-"erun" tenant with a local-agent or
// remote-agent env). The toggle/persist contract is covered by Go unit tests
// on contributeStore and the SetContributeMode flow.

test.describe('titlebar contribute toggle', () => {
  test('stays hidden when no environment is selected', async ({ page }) => {
    await expect(page.getByRole('button', { name: /Contribute to ERun/i })).toHaveCount(0);
    await expect(page.getByRole('button', { name: /Disable contribute mode/i })).toHaveCount(0);
  });

  test('appears for an eligible local-agent or remote-agent env', async ({ app, page }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    const toggle = page.getByRole('button', {
      name: /Contribute to ERun|Disable contribute mode/i,
    });
    await expect(toggle.first()).toBeVisible();
  });
});
