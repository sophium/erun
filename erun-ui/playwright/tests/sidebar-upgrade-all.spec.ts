import { test, expect } from '../fixtures/erunApp.js';

// The sidebar "Upgrade all" button opens a preview dialog that
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
    await expect(dialog).toBeVisible();

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

  // The dialog must render the same three-way outcome the CLI's
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
  // The fourth row is a snapshot-channel member whose
  // target is a stable release, because the stable was published on top of
  // the latest snapshot (the resolver decision is owned by the upgrade
  // dry-run goldens; this locks how the dialog renders the resulting row).
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

    // Lagging env → "will upgrade", showing current → target.
    const laggingRow = dialog.locator('tr', { hasText: 'lagging-env' });
    await expect(laggingRow).toContainText('will upgrade');
    await expect(laggingRow).toContainText('1.0.86-snapshot-20260606090936');

    // Known latest, current matches → "up to date".
    const currentRow = dialog.locator('tr', { hasText: 'current-env' });
    await expect(currentRow).toContainText('up to date');

    // Unresolved target → "latest unknown", and NOT "up to date" (the
    // regression this fix prevents). The current version is still shown; the
    // unknown target lives in the Status column, not as a "(unset)" target —
    // and the row carries the actual failure reason, so the
    // operator sees why without leaving the dialog.
    const unresolvedRow = dialog.locator('tr', { hasText: 'unresolved-env' });
    await expect(unresolvedRow).toContainText('latest unknown');
    await expect(unresolvedRow).toContainText('ghcr token request failed: 403 Forbidden');
    await expect(unresolvedRow).toContainText('1.0.80-snapshot-20260101000000');
    await expect(unresolvedRow).not.toContainText('up to date');

    // Snapshot-channel member whose stream was superseded by a stable release:
    // the row keeps its snapshot channel but proposes the
    // stable version, and counts as a regular upgrade.
    const stableAdoptRow = dialog.locator('tr', { hasText: 'stable-adopt-env' });
    await expect(stableAdoptRow).toContainText('snapshot');
    await expect(stableAdoptRow).toContainText('1.0.87');
    await expect(stableAdoptRow).toContainText('will upgrade');

    // Summary counts only the lagging members, and a distinct line explains
    // why an opted-in env will not be redeployed.
    await expect(dialog).toContainText('2 of 4 will be redeployed.');
    await expect(dialog).toContainText('be checked against the latest version');

    // Only the lagging envs are upgradable; the button names that count and
    // stays enabled.
    await expect(dialog.getByRole('button', { name: 'Upgrade 2' })).toBeEnabled();

    // Capture the populated plan for visual review of the layout.
    await dialog.screenshot({ path: 'test-results/upgrade-all-plan.png' });

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
  });

  // Confirming Upgrade all fans out per member: every lagging
  // env runs its own scoped `erun upgrade --tenant <t> --environment <e>` in
  // its OWN Local shell (in parallel), instead of one global run executing
  // serially in a single host env's terminal. And the fan-out must not drag
  // in each env's default tab set — in particular no AI tab, whose spawn
  // used to launch a Claude session as a side effect of confirming.
  //
  // Every Start* RPC is stubbed so neither the real `erun upgrade` nor any
  // real session spawn fires; the spec asserts one scoped dispatch per
  // lagging member (and only those), zero AI-session spawns, and that the
  // pre-fix block message never appears. The real parallel deploys are not
  // stageable headless; the per-env command composition is owned by the
  // upgrade dry-run goldens (dry_run_scoped_flags_lagging) and the Go
  // resolver tests.
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
      // Defuse every session spawn, counting the ones under test. AI spawns
      // are counted only for the plan's (fake) tenants: the singleton
      // backend's dead-tab respawn machinery may legitimately revive the AI
      // tab of whatever REAL env earlier specs left selected, and that
      // background churn is not the invariant here.
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

    // One scoped run per lagging member, each in its own env — never the
    // up-to-date member, never a single global run.
    await expect.poll(() => upgradeRuns.length).toBe(2);
    const ran = upgradeRuns
      .map((selection) => `${selection.tenant ?? ''}/${selection.environment ?? ''}`)
      .sort();
    expect(ran).toEqual(['acme/lagging-a', 'beta/lagging-b']);

    // No member had an AI tab spawned as a side effect of confirming.
    expect(memberAISessions).toEqual([]);

    // The pre-fix block message must never appear.
    await expect(page.getByText('No environments are configured to upgrade')).toHaveCount(0);
    await expect(page.getByText('Open an environment first')).toHaveCount(0);
    await expect(dialog).toBeHidden();
  });

  // When an env's listed registries offer more than one newer
  // version, Upgrade all must not silently auto-pick: the row carries every
  // distinct candidate (each labelled with its source registry) and the
  // operator picks one before it can be redeployed. The CLI/MCP skip such an
  // env as ambiguous; the desktop is where the pick happens. The candidate set
  // comes from a registry fan-out that the headless harness can't stage (it
  // needs two registries publishing different versions), so we stub
  // ResolveUpgradePlan over /__erun_invoke — the same technique the rows above
  // use; the resolver decision is owned by the Go tests
  // (TestResolveUpgradePlanOffersCandidatesWhenRegistriesDisagree) and the
  // confirmed --version composition by buildUpgradeArgs.
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

    // Until a version is picked the env asks for one and is not in the count;
    // Upgrade stays disabled (nothing to redeploy yet).
    const pickRow = dialog.locator('tr', { hasText: 'pick-env' });
    await expect(pickRow).toContainText('pick a version');
    await expect(dialog).toContainText('0 of 1 will be redeployed.');
    await expect(dialog.getByRole('button', { name: 'Upgrade', exact: true })).toBeDisabled();

    // Pick the public registry's newer version from the per-row selector.
    await pickRow.getByLabel('Pick a version for pick-env').click();
    await page.getByRole('option', { name: new RegExp(newer) }).click();

    // The env now joins the upgrade set and the button enables + names the count.
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
