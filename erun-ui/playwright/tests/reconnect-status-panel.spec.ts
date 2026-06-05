import { expect, test } from '../fixtures/erunApp.js';

// ReconnectStatusPanel renders in #431's fix: the in-flight ("running") and
// failure ("error") states of the review-pane reconnect/redeploy used to live
// inside a shadcn Dialog (`ReconnectDialog`) that overlaid the whole app, so
// while one env was redeploying every other env's sidebar row, tab, and
// terminal were unreachable. The fix splits the flow into two surfaces:
//
//   1) ReconnectDialog — confirmation-only modal, status === 'confirm'.
//   2) ReconnectStatusPanel — non-modal floating panel anchored bottom-right,
//      scoped to the env recorded in state.review.reconnect.{tenant,
//      environment}. Renders only when status is 'running' or 'error'.
//
// Staging a real reconnect requires the backend's `ReconnectMCP` to actually
// run `erun open --no-shell` against a deployed runtime, which the headless
// harness can't reproduce — same constraint that `diff-error-copy.spec.ts`
// already records. So this spec locks the reachable negative invariants:
//
//   - In steady state (no reconnect in flight), the panel must not be in the
//     DOM and the sidebar must not block interaction with other envs.
//   - The existing dialog mount remains confirmation-only — when there is no
//     reconnect in flight, no Dialog labelled "Reconnect to environment?" is
//     visible.
//
// The positive path — that handleReconnectLine appends to a capped buffer,
// that confirmReconnect transitions to status==='running' and clears the
// buffer, and that ReconnectMCP streams real PTY output — is covered by
// `TestReconnectMCPRunsOpenAndStreamsLines` in `erun-ui/app_test.go` plus
// visual review of the surface during PR sign-off.

test.describe('reconnect status panel', () => {
  test('panel is not mounted in the healthy state', async ({ page }) => {
    // The panel is a status region carrying data-reconnect-status; it is
    // rendered conditionally on review.reconnect.status, so its absence is
    // the structural assertion that the fix did not regress the gate.
    await expect(page.locator('[data-reconnect-status]')).toHaveCount(0);
    // The non-modal panel is not part of any Dialog. Belt-and-braces check
    // that the previous modal-only mount is gone: there must be no dialog
    // titled "Reconnect to environment?" until the user triggers one.
    await expect(page.getByRole('dialog', { name: 'Reconnect to environment?' })).toHaveCount(0);
  });

  test('sidebar rows stay interactive when no reconnect is running', async ({ app, page }) => {
    // Before the fix, the reconnect modal blocked ALL envs (not just the one
    // being reconnected). With no reconnect in flight the sidebar rows must
    // be reachable as clickable buttons — no modal overlay, no
    // pointer-events:none, no aria-busy on the chrome.
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);

    const sidebar = page.locator('aside').first();
    const firstRow = sidebar.getByRole('button', { name: new RegExp(`^${tenant} / ${envs[0]!}`) });
    await expect(firstRow).toBeVisible();
    await expect(firstRow).toBeEnabled();
  });
});
