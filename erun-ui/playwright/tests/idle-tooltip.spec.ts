import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// idle-tooltip covers the titlebar idle widget's mount gate. The seeded
// baseline envs are inert local-agent envs with no managed cloud context,
// so the deterministic invariant here is the negative one: selecting such
// an env must never mount the idle-status badge (and therefore no tooltip).
// The populated tooltip (per-IP SSH client rows under the SSH marker) needs
// a live managed cloud context plus real proxy traffic, which the isolated
// harness cannot stage; that projection is covered by the Go unit tests
// TestActivityIdleMarkerProjectsClientsSortedByRecency and
// TestIdleStatusToUIProjectsMarkerClients, plus the integration scenario
// status_json_includes_per_client_breakdown. The mocked-RPC positive path
// of the widget itself is exercised by idle-widget-stop-protection.spec.ts.

test.describe('idle tooltip', () => {
  test('idle widget stays unmounted for an env without a managed cloud context', async ({
    app,
    page,
  }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // The widget only mounts after a LoadIdleStatus poll reports a managed
    // cloud context. The seeded env has none, so the badge must not appear.
    // Give the first poll a window to land before asserting — toBeHidden
    // alone would pass instantly and prove nothing about the poll result.
    await page.waitForTimeout(2_000);
    const badge = app.titlebar.idleStatusBadge();
    await expect(badge).toBeHidden();
  });
});
