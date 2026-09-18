import { test, expect } from '../../../fixtures/erunApp.js';

// "Upgrade all" previews a cross-env upgrade plan before any deploy. This spec
// owns the desktop dialog surface; the CLI dry-run goldens own plan resolution
// and deploy composition.
test.describe('sidebar Upgrade all', () => {
  test('the Upgrade all button opens the preview dialog and cancels cleanly', async ({ app }) => {
    await app.sidebar.openUpgradeAll();

    const dialog = app.sidebar.upgradeAllDialog();
    await expect(dialog).toBeVisible();

    // Guards against a dialog stuck on the loading spinner: the body must
    // settle into a plan table or the opted-in empty state.
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

  // The dialog must render the same three-way outcome as the CLI's
  // `erun upgrade`: an opted-in env lags a known channel latest, sits at the
  // latest, or has no resolvable target. That third case once rendered as "up
  // to date" with a "(unset)" target, mislabelling a failed lookup as success
  // and making Upgrade all look like it did nothing — this test guards it.
  //
  // The unresolved case needs a tenant whose registry lookup fails, which the
  // headless harness can't stage, so the plan is stubbed over /__erun_invoke.
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
          unresolvedReason: 'ghcr token request failed: 403 Forbidden',
        },
        {
          tenant: 'acme',
          environment: 'stable-adopt-env',
          channel: 'snapshot',
          current: '1.0.86-snapshot-20260606090936',
          target: '1.0.87',
          lagging: true,
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
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('table', { name: 'Upgrade plan' })).toBeVisible();

    const laggingRow = dialog.locator('tr', { hasText: 'lagging-env' });
    await expect(laggingRow).toContainText('will upgrade');
    await expect(laggingRow).toContainText('1.0.86-snapshot-20260606090936');

    const currentRow = dialog.locator('tr', { hasText: 'current-env' });
    await expect(currentRow).toContainText('up to date');

    // The failure reason shows inline so the operator sees why the target is
    // unknown without leaving the dialog — not the old "up to date" mislabel.
    const unresolvedRow = dialog.locator('tr', { hasText: 'unresolved-env' });
    await expect(unresolvedRow).toContainText('latest unknown');
    await expect(unresolvedRow).toContainText('ghcr token request failed: 403 Forbidden');
    await expect(unresolvedRow).toContainText('1.0.80-snapshot-20260101000000');
    await expect(unresolvedRow).not.toContainText('up to date');

    // A snapshot member whose stream was superseded by a stable release keeps
    // its channel but adopts the stable version as a regular upgrade.
    const stableAdoptRow = dialog.locator('tr', { hasText: 'stable-adopt-env' });
    await expect(stableAdoptRow).toContainText('snapshot');
    await expect(stableAdoptRow).toContainText('1.0.87');
    await expect(stableAdoptRow).toContainText('will upgrade');

    // The summary counts only lagging members; a separate line explains why an
    // opted-in env won't be redeployed.
    await expect(dialog).toContainText('2 of 4 will be redeployed.');
    await expect(dialog).toContainText('be checked against the latest version');

    await expect(dialog.getByRole('button', { name: 'Upgrade 2' })).toBeEnabled();

    // Capture the populated plan for visual review of the layout.
    await dialog.screenshot({ path: 'test-results/upgrade-all-plan.png' });

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
  });

  // Confirming fans out per member: each lagging env runs its own scoped
  // `erun upgrade` in its own Local shell, in parallel — not one global run in
  // a single env's terminal. The fan-out must not drag in each env's default
  // tabs; the AI tab in particular once spawned a Claude session as a side
  // effect of confirming, and this guards against that.
  //
  // Real parallel deploys aren't stageable headless, so every Start* RPC is
  // stubbed; per-env command composition is owned by the upgrade dry-run
  // goldens and the Go resolver tests.
  test('confirming fans out one scoped run per lagging member, with no AI spawn', async ({
    app,
    page,
  }) => {
    const plan = {
      items: [
        {
          tenant: 'acme',
          environment: 'lagging-a',
          channel: 'snapshot',
          current: '1.0.0-snapshot-20260101000000',
          target: '1.0.0-snapshot-20260102000000',
          lagging: true,
        },
        {
          tenant: 'beta',
          environment: 'lagging-b',
          channel: 'stable',
          current: '1.0.0',
          target: '1.1.0',
          lagging: true,
        },
        {
          tenant: 'acme',
          environment: 'current-env',
          channel: 'stable',
          current: '1.1.0',
          target: '1.1.0',
          lagging: false,
        },
      ],
    };
    const upgradeRuns: Array<{ tenant?: string; environment?: string }> = [];
    const memberAISessions: string[] = [];
    let nextSessionId = 100;

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
      // Count AI spawns only for the plan's fake tenants: the singleton backend
      // may legitimately revive the AI tab of a real env an earlier spec left
      // selected, and that background churn is not the invariant under test.
      if (body.method.startsWith('Start')) {
        const selection = body.args[0] ?? {};
        if (body.method === 'StartUpgradeEnvironmentSession') {
          upgradeRuns.push(selection);
        }
        if (
          body.method === 'StartAISession' &&
          (selection.tenant === 'acme' || selection.tenant === 'beta')
        ) {
          memberAISessions.push(`${selection.tenant ?? ''}/${selection.environment ?? ''}`);
        }
        nextSessionId++;
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              sessionId: nextSessionId,
              selection,
              slot: 0,
              kind: 'local',
            },
          }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openUpgradeAll();
    const dialog = app.sidebar.upgradeAllDialog();
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Upgrade 2' }).click();

    await expect.poll(() => upgradeRuns.length).toBe(2);
    const ran = upgradeRuns
      .map((selection) => `${selection.tenant ?? ''}/${selection.environment ?? ''}`)
      .sort();
    expect(ran).toEqual(['acme/lagging-a', 'beta/lagging-b']);

    expect(memberAISessions).toEqual([]);

    // The pre-fix block message must never appear.
    await expect(page.getByText('No environments are configured to upgrade')).toHaveCount(0);
    await expect(page.getByText('Open an environment first')).toHaveCount(0);
    await expect(dialog).toBeHidden();
  });

  // When an env's registries offer more than one newer version, Upgrade all
  // must not silently auto-pick: the row carries every candidate (labelled by
  // source registry) and the operator picks one before it can be redeployed.
  // CLI/MCP skip such an env as ambiguous; the desktop is where the pick
  // happens. Two disagreeing registries aren't stageable headless, so the plan
  // is stubbed over /__erun_invoke.
  test('an ambiguous env offers a per-registry pick that drives the chosen version', async ({
    app,
    page,
  }) => {
    const newer = '1.0.90-snapshot-20260606090000';
    const older = '1.0.86-snapshot-20260606082157';
    const plan = {
      items: [
        {
          tenant: 'acme',
          environment: 'pick-env',
          channel: 'snapshot',
          current: '1.0.80-snapshot-20260101000000',
          target: '',
          lagging: false,
          candidates: [
            { version: newer, registry: 'ghcr.io/acme' },
            { version: older, registry: 'registry.internal/acme' },
          ],
          unresolvedReason: 'multiple newer versions across registries; pick one or pass --version',
        },
      ],
    };
    const upgradeRuns: Array<{ tenant?: string; environment?: string; version?: string }> = [];
    let nextSessionId = 200;

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as {
        method: string;
        args: Array<{ tenant?: string; environment?: string; version?: string }>;
      };
      if (body.method === 'ResolveUpgradePlan') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: plan }),
        });
      }
      if (body.method.startsWith('Start')) {
        const selection = body.args[0] ?? {};
        if (body.method === 'StartUpgradeEnvironmentSession') {
          upgradeRuns.push(selection);
        }
        nextSessionId++;
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: { sessionId: nextSessionId, selection, slot: 0, kind: 'local' },
          }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openUpgradeAll();
    const dialog = app.sidebar.upgradeAllDialog();
    await expect(dialog).toBeVisible();

    const pickRow = dialog.locator('tr', { hasText: 'pick-env' });
    await expect(pickRow).toContainText('pick a version');
    await expect(dialog).toContainText('0 of 1 will be redeployed.');
    await expect(dialog.getByRole('button', { name: 'Upgrade', exact: true })).toBeDisabled();

    await pickRow.getByLabel('Pick a version for pick-env').click();
    await page.getByRole('option', { name: new RegExp(newer) }).click();

    await expect(pickRow).toContainText('will upgrade');
    await expect(dialog).toContainText('1 of 1 will be redeployed.');
    const upgradeButton = dialog.getByRole('button', { name: 'Upgrade 1' });
    await expect(upgradeButton).toBeEnabled();
    await upgradeButton.click();

    // The confirmed run carries the picked version so `erun upgrade` deploys
    // exactly what the operator chose.
    await expect.poll(() => upgradeRuns.length).toBe(1);
    expect(upgradeRuns[0]?.environment).toBe('pick-env');
    expect(upgradeRuns[0]?.version).toBe(newer);
    await expect(dialog).toBeHidden();
  });
});
