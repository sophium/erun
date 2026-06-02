import { expect, test } from '../fixtures/erunApp.js';

// sidebar-open-dot covers the per-env green dot that signals
// "this env has tabs open in the desktop" and is clickable to
// close the env (tear down its Local/ERun/AI tabs and stop
// tracking it from the desktop session state). The dot is
// independent of the LOCAL pill (which marks the dev-machine env)
// and of the busy spinner (which fires only while an
// activity-queue entry is running): open and busy are independent
// states and can coexist.
//
// The dot is driven by state.terminal.tabsByEnv[selectionKey]
// having at least one entry. The headless harness exercises the
// same openSelection thunk a real click hits, so the dot mounts
// after openEnvironment and disappears after the close click.

test.describe('sidebar env open dot', () => {
  test('quiet env rows show no open dot', async ({ app, page }) => {
    // Steady state: no env has been clicked yet in this fresh harness,
    // so no tabsByEnv entries exist, so no open dot should render. The
    // negative invariant pins the "default off" branch of the new
    // selector so a regression that flipped the predicate (e.g. NOT
    // null-checking the map lookup) would fail loudly here.
    const tenants = await app.sidebar.tenants();
    test.skip(tenants.length === 0, 'no tenants in this developer harness');
    const sidebar = page.locator('aside').first();
    await expect(sidebar.getByTestId('env-open-dot')).toHaveCount(0);
  });

  test('opening an env mounts the dot; clicking the dot closes the env', async ({ app, page }) => {
    const tenants = await app.sidebar.tenants();
    test.skip(tenants.length === 0, 'no tenants in this developer harness');
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    test.skip(envs.length === 0, `no envs under tenant ${tenant}`);
    const env = envs[0]!;

    await app.sidebar.openEnvironment(tenant, env);

    // Scope the dot lookup to the same env row to avoid collisions if
    // multiple envs end up open across the suite — the row containing
    // the matching edit button is the row this test owns.
    const sidebar = page.locator('aside').first();
    const dot = sidebar.getByRole('button', { name: `Close ${tenant} / ${env}` });
    await expect(dot).toBeVisible({ timeout: 6_000 });

    // Clicking the dot must not also trigger the row's openSelection.
    // The selected env after the close should NOT remain on the one
    // we just closed; we assert the dot disappears, which is the
    // observable signal that tabsByEnv was cleared.
    await dot.click();
    await expect(dot).toHaveCount(0, { timeout: 6_000 });
  });
});
