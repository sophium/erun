import { test, expect } from '../fixtures/erunApp.js';

// Issue #440 — the sidebar "Upgrade all" button opens a preview dialog that
// resolves the cross-env upgrade plan (every opted-in env, its channel, and
// current → target) before any deploy. This spec drives the reachable surface:
// the button opens the dialog, the dialog renders either the plan table or the
// "no environments opted in" empty state, and Cancel closes it without
// deploying. The populated-plan path (lagging envs → confirm → deploy) depends
// on opted-in envs in ~/.erun and on a registry lookup, so it is verified
// end-to-end against a fixture in the PR; the CLI dry-run goldens own the plan
// resolution + deploy composition.
test.describe('sidebar Upgrade all', () => {
  test('the Upgrade all button opens the preview dialog and cancels cleanly', async ({ app }) => {
    await app.sidebar.openUpgradeAll();

    const dialog = app.sidebar.upgradeAllDialog();
    await expect(dialog).toBeVisible({ timeout: 6_000 });

    // The body resolves to one of the two terminal states (never stuck on the
    // loading spinner): a plan table, or the opted-in empty state.
    await expect
      .poll(async () => {
        const hasTable = await dialog.getByRole('table', { name: 'Upgrade plan' }).count();
        const hasEmpty = await dialog
          .getByText('No environments are opted into Upgrade all')
          .count();
        return hasTable + hasEmpty;
      })
      .toBeGreaterThan(0);

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
  });

  // Issue #459 — the dialog must render the same three-way outcome the CLI's
  // `erun upgrade` already shows: an opted-in env either lags a known channel
  // latest, sits at the known latest, or has no resolvable target (registry
  // lookup failed / no matching tags). The third case previously rendered as
  // "up to date" with a "(unset)" target, which mislabelled a failed lookup as
  // success and made Upgrade all look like it was doing nothing.
  //
  // The unresolved state can't be staged through the real backend (it needs a
  // tenant whose runtime-image registry lookup fails), so we stub the
  // ResolveUpgradePlan RPC over the /__erun_invoke bridge — the same technique
  // idle-widget-stop-protection.spec.ts uses — to drive a deterministic plan
  // with one item in each state. Every other RPC passes through untouched.
  test('an unresolved channel target renders as "latest unknown", not "up to date"', async ({
    app,
    page,
  }) => {
    const plan = {
      items: [
        {
          tenant: 'acme',
          environment: 'lagging-env',
          channel: 'stable',
          current: '1.0.0',
          target: '1.2.0',
          lagging: true,
        },
        {
          tenant: 'acme',
          environment: 'current-env',
          channel: 'stable',
          current: '1.2.0',
          target: '1.2.0',
          lagging: false,
        },
        {
          tenant: 'acme',
          environment: 'unresolved-env',
          channel: 'snapshot',
          current: '1.0.86-snapshot-20260605185826',
          target: '',
          lagging: false,
        },
      ],
    };

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'ResolveUpgradePlan') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: plan }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openUpgradeAll();
    const dialog = app.sidebar.upgradeAllDialog();
    await expect(dialog).toBeVisible({ timeout: 6_000 });
    await expect(dialog.getByRole('table', { name: 'Upgrade plan' })).toBeVisible();

    // Lagging env → "will upgrade".
    const laggingRow = dialog.locator('tr', { hasText: 'lagging-env' });
    await expect(laggingRow).toContainText('will upgrade');

    // Known latest, current matches → "up to date".
    const currentRow = dialog.locator('tr', { hasText: 'current-env' });
    await expect(currentRow).toContainText('up to date');

    // Unresolved target → "latest unknown", and NOT "up to date" (the
    // regression this fix prevents). The target still renders "(unset)".
    const unresolvedRow = dialog.locator('tr', { hasText: 'unresolved-env' });
    await expect(unresolvedRow).toContainText('latest unknown');
    await expect(unresolvedRow).toContainText('(unset)');
    await expect(unresolvedRow).not.toContainText('up to date');

    // Summary counts only the lagging member, and a distinct line explains why
    // an opted-in env will not be redeployed.
    await expect(dialog).toContainText('1 of 3 will be redeployed.');
    await expect(dialog).toContainText('be checked against the latest version');

    // Only the one lagging env is upgradable; the button names that count and
    // stays enabled.
    await expect(dialog.getByRole('button', { name: 'Upgrade 1' })).toBeEnabled();

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
  });
});
