import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The click fires a real MCP call the harness cannot stage, so only the
// visibility gating is covered here; toggle/persist is covered in Go.

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
