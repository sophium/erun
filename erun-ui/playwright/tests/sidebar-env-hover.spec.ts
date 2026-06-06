import { test, expect } from '../fixtures/erunApp.js';

// Issue #437 — hovering a sidebar env row shows a hover card with the env's
// version, the issue it's working on (branch + linked issue title), and its
// current activity, replacing the plain tenant/env tooltip.
//
// The three section labels and the empty/populated states are reachable
// against any config. The branch+title content depends on the env's worktree
// being on the host (local-agent); the dev's real ~/.erun may only have
// remote-worktree envs, so the spec asserts the card structure + that the
// "Working on" section renders *some* resolved state (branch, an availability
// reason, or "Resolving…"), not a specific branch. The populated branch+issue
// path is verified end-to-end against a local-agent fixture in the PR.
test.describe('sidebar env hover card', () => {
  test('hovering an env row opens a card with version, working issue, and activity', async ({
    app,
  }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);
    const env = envs[0]!;

    await app.sidebar.hoverEnvironmentRow(tenant, env);

    const card = app.sidebar.envHoverCard(tenant, env);
    await expect(card).toBeVisible({ timeout: 6_000 });

    // All three sections are present.
    await expect(card.getByText('Version', { exact: true })).toBeVisible();
    await expect(card.getByText('Working on', { exact: true })).toBeVisible();
    await expect(card.getByText('Activity', { exact: true })).toBeVisible();

    // The working-issue lookup resolves to a non-empty state (a branch, an
    // availability reason for remote envs, or the transient loading text) —
    // never a blank.
    await expect
      .poll(async () => (await card.locator('dd').nth(1).textContent())?.trim() ?? '')
      .not.toBe('');
  });
});
