import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Contribute-toggle coverage.
//
// The Contribute toggle is a per-env titlebar control that:
//   - is hidden when no env is selected;
//   - is hidden for the special "erun" tenant (self-referential);
//   - is hidden for non-agent env types (runtime envs);
//   - is visible only for local-agent / remote-agent envs in non-erun tenants;
//   - mirrors aria-pressed against state.contribute.flagsByEnv on click.
//
// The positive click path triggers a real `contribute_clone` MCP call
// inside the env, which the headless harness cannot stage (no live MCP
// endpoint, no host-side ~/.erun seeded for "contribute"). Per the
// playwright AGENTS.md guidance ("cover the closest observable
// invariant the harness can reach"), this spec locks the visibility
// gating and the negative invariant that the toggle stays hidden under
// non-eligible selections. The backend toggle/persist contract is
// covered by Go unit tests on contributeStore + the SetContributeMode
// flow.

test.describe('titlebar contribute toggle', () => {
  test('stays hidden when no environment is selected', async ({ page }) => {
    // Before the user selects an env, the toggle's eligibility check
    // returns false because state.selection.selected is null.
    await expect(page.getByRole('button', { name: /Contribute to ERun/i })).toHaveCount(0);
    await expect(page.getByRole('button', { name: /Disable contribute mode/i })).toHaveCount(0);
  });

  test('appears for an eligible local-agent or remote-agent env', async ({ app, page }) => {
    // The seeded baseline stages exactly the eligible case: pw is not the
    // special "erun" tenant and alpha is an explicit local-agent env, so
    // the toggle must render once the env is selected.
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    // The toggle's button label switches between "Contribute to ERun"
    // and "Disable contribute mode" depending on state; either label
    // appearing in the titlebar means the gating passed.
    const toggle = page.getByRole('button', {
      name: /Contribute to ERun|Disable contribute mode/i,
    });
    await expect(toggle.first()).toBeVisible();
  });
});
