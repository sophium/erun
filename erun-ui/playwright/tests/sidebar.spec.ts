import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

test.describe('sidebar', () => {
  test('tenant toggle flips aria-expanded', async ({ app }) => {
    const before = await app.sidebar.isTenantExpanded(SEED_TENANT);
    await app.sidebar.toggleTenant(SEED_TENANT);
    const after = await app.sidebar.isTenantExpanded(SEED_TENANT);
    expect(after).toBe(!before);
    // Restore state so subsequent assertions in the suite don't drift.
    await app.sidebar.toggleTenant(SEED_TENANT);
  });

  test('opening an environment surfaces status feedback', async ({ app, page }) => {
    // The opening status is inherently transient — it lives only for the open
    // itself (a few hundred ms): the titlebar "Opening …" message clears the
    // instant the session's first output lands, and the sidebar row's busy
    // spinner (role=status, its aria-label naming the env) clears when the open
    // settles. Start asserting CONCURRENTLY with the click rather than after it —
    // awaiting the click first can consume the whole window on a fast open and
    // race the feedback away. Match either surface so it is deterministic across
    // hosts; the spinner's wording varies ("Opening …" before the open action
    // starts running, "Working on …" once it is), so match it by the env name.
    const target = `${SEED_TENANT} / ${SEED_ENV_ALPHA}`;
    const opening = app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    await expect(
      page
        .getByText(`Opening ${target}`, { exact: false })
        .or(page.locator(`[role="status"][aria-label*="${target}"]`))
        .first(),
    ).toBeVisible({
      timeout: 15_000,
    });
    await opening;
  });
});
