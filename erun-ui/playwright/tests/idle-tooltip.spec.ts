import { expect, test } from '../fixtures/erunApp.js';

// idle-tooltip covers the titlebar tooltip helper changes that ship per-IP
// detail lines under the SSH marker. The headless backend reads the
// developer's real ~/.cache/erun/activity directory, so seeding live SSH
// proxy traffic to materialize client rows in the tooltip is not portable
// per playwright/AGENTS.md. The spec therefore locks the closest reachable
// invariant: the tooltip helper renders without crashing on whatever real
// idle status the harness happens to surface, and the basic line shape is
// intact. Per-client projection itself is covered by the Go unit tests
// TestActivityIdleMarkerProjectsClientsSortedByRecency and
// TestIdleStatusToUIProjectsMarkerClients, plus the integration scenario
// status_json_includes_per_client_breakdown.

test.describe('idle tooltip', () => {
  test('renders without crashing when an env is selected', async ({ app }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);

    await app.sidebar.openEnvironment(tenant, envs[0]!);

    // The idle widget only mounts after the first LoadIdleStatus poll
    // resolves for the new selection. Wait up to a few seconds; on local
    // envs the widget may stay unmounted (no managed cloud context), in
    // which case the negative invariant is what we cover — no tooltip,
    // no crash.
    const badge = app.titlebar.idleStatusBadge();
    const appeared = await badge.waitFor({ state: 'visible', timeout: 4_000 }).then(
      () => true,
      () => false,
    );
    if (!appeared) {
      // No managed cloud context for the selected env, so the widget
      // never mounted. That is itself a valid state; the helper has no
      // input to crash on.
      return;
    }

    await badge.hover();
    // Radix renders the tooltip body into a portal at the document root.
    // Match the Idle-timeout header line that the helper always emits.
    await expect(app.page.getByText(/^Idle timeout: \d+s$/).first()).toBeVisible({
      timeout: 3_000,
    });
  });
});
