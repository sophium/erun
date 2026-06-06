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
    // Realistic long *-snapshot-<timestamp> tags (the layout has to stay
    // readable for these without wrapping mid-token).
    const plan = {
      items: [
        {
          tenant: 'acme',
          environment: 'lagging-env',
          channel: 'snapshot',
          current: '1.0.86-snapshot-20260606082157',
          target: '1.0.86-snapshot-20260606090936',
          lagging: true,
        },
        {
          tenant: 'acme',
          environment: 'current-env',
          channel: 'stable',
          current: '1.0.85',
          target: '1.0.85',
          lagging: false,
        },
        {
          tenant: 'acme',
          environment: 'unresolved-env',
          channel: 'snapshot',
          current: '1.0.80-snapshot-20260101000000',
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

    // Lagging env → "will upgrade", showing current → target.
    const laggingRow = dialog.locator('tr', { hasText: 'lagging-env' });
    await expect(laggingRow).toContainText('will upgrade');
    await expect(laggingRow).toContainText('1.0.86-snapshot-20260606090936');

    // Known latest, current matches → "up to date".
    const currentRow = dialog.locator('tr', { hasText: 'current-env' });
    await expect(currentRow).toContainText('up to date');

    // Unresolved target → "latest unknown", and NOT "up to date" (the
    // regression this fix prevents). The current version is still shown; the
    // unknown target lives in the Status column, not as a "(unset)" target.
    const unresolvedRow = dialog.locator('tr', { hasText: 'unresolved-env' });
    await expect(unresolvedRow).toContainText('latest unknown');
    await expect(unresolvedRow).toContainText('1.0.80-snapshot-20260101000000');
    await expect(unresolvedRow).not.toContainText('up to date');

    // Summary counts only the lagging member, and a distinct line explains why
    // an opted-in env will not be redeployed.
    await expect(dialog).toContainText('1 of 3 will be redeployed.');
    await expect(dialog).toContainText('be checked against the latest version');

    // Only the one lagging env is upgradable; the button names that count and
    // stays enabled.
    await expect(dialog.getByRole('button', { name: 'Upgrade 1' })).toBeEnabled();

    // Capture the populated plan for visual review of the layout.
    await dialog.screenshot({ path: 'test-results/upgrade-all-plan.png' });

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
  });

  // Issue #459 follow-up — confirming Upgrade all must run the global
  // `erun upgrade` against an automatically-resolved host environment instead
  // of telling the operator to "open an environment first". `erun upgrade`
  // redeploys every opted-in env itself; the host env only supplies the Local
  // shell to run it in. The fixture boots with no environment selected (the
  // exact state that previously blocked), so clicking Upgrade exercises the
  // host-resolution fallback.
  //
  // Every Start* RPC is stubbed so neither the real `erun upgrade` nor any real
  // session spawn fires; the spec asserts the command was dispatched with a
  // resolved host and the pre-fix block message never appears.
  test('confirming runs without requiring an open environment', async ({ app, page }) => {
    const plan = {
      items: [
        {
          tenant: 'acme',
          environment: 'lagging-env',
          channel: 'snapshot',
          current: '1.0.0-snapshot-20260101000000',
          target: '1.0.0-snapshot-20260102000000',
          lagging: true,
        },
      ],
    };
    const upgradeHosts: Array<{ tenant?: string; environment?: string }> = [];

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as {
        method: string;
        args: Array<{ tenant?: string; environment?: string }>;
      };
      if (body.method === 'ResolveUpgradePlan') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: plan }),
        });
      }
      // Defuse the real `erun upgrade` and any dependent-tab session spawns the
      // confirm path triggers, returning a benign session result for each.
      if (body.method.startsWith('Start')) {
        if (body.method === 'StartUpgradeAllSession') {
          upgradeHosts.push(body.args[0] ?? {});
        }
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: { sessionId: 1, selection: body.args[0] ?? {}, slot: 0, kind: 'local' },
          }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openUpgradeAll();
    const dialog = app.sidebar.upgradeAllDialog();
    await expect(dialog).toBeVisible({ timeout: 6_000 });
    await dialog.getByRole('button', { name: 'Upgrade 1' }).click();

    // The command runs from the environment being upgraded (the lagging plan
    // member), not an unrelated env, and it did not block on a missing
    // selection.
    await expect.poll(() => upgradeHosts.length).toBeGreaterThan(0);
    expect(upgradeHosts[0]?.tenant).toBe('acme');
    expect(upgradeHosts[0]?.environment).toBe('lagging-env');

    // The pre-fix block message must never appear.
    await expect(page.getByText('No environments are configured to upgrade')).toHaveCount(0);
    await expect(page.getByText('Open an environment first')).toHaveCount(0);
    await expect(dialog).toBeHidden();
  });
});
