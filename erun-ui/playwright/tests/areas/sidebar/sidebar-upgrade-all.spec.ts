import { artifactPath } from '../../../fixtures/artifacts.js';
import { test, expect } from '../../../fixtures/erunApp.js';

// This spec owns the dialog surface; CLI dry-run goldens own plan resolution.
test.describe('sidebar Upgrade all', () => {
  test('the Upgrade all button opens the preview dialog and cancels cleanly', async ({ app }) => {
    await app.sidebar.openUpgradeAll();

    const dialog = app.sidebar.upgradeAllDialog();
    await expect(dialog).toBeVisible();

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

  // An unresolvable target must not render as "up to date". The plan is stubbed
  // because a failing registry lookup cannot be staged headless.
  test('an unresolved channel target renders as "latest unknown", not "up to date"', async ({
    app,
    page,
  }) => {
    // Long snapshot tags must not wrap mid-token.
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

    const unresolvedRow = dialog.locator('tr', { hasText: 'unresolved-env' });
    await expect(unresolvedRow).toContainText('latest unknown');
    await expect(unresolvedRow).toContainText('ghcr token request failed: 403 Forbidden');
    await expect(unresolvedRow).toContainText('1.0.80-snapshot-20260101000000');
    await expect(unresolvedRow).not.toContainText('up to date');

    // A snapshot member superseded by a stable release adopts it as an upgrade.
    const stableAdoptRow = dialog.locator('tr', { hasText: 'stable-adopt-env' });
    await expect(stableAdoptRow).toContainText('snapshot');
    await expect(stableAdoptRow).toContainText('1.0.87');
    await expect(stableAdoptRow).toContainText('will upgrade');

    await expect(dialog).toContainText('2 of 4 will be redeployed.');
    await expect(dialog).toContainText('be checked against the latest version');

    await expect(dialog.getByRole('button', { name: 'Upgrade 2' })).toBeEnabled();

    await dialog.screenshot({ path: artifactPath('test-results/upgrade-all-plan.png') });

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
  });

  // Each lagging env runs its own scoped `erun upgrade` in its Local shell, with
  // no default-tab (AI) spawn. Start* RPCs are stubbed.
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
      // Only the plan's fake tenants count: the backend may revive a real env's AI tab.
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

    await expect(page.getByText('No environments are configured to upgrade')).toHaveCount(0);
    await expect(page.getByText('Open an environment first')).toHaveCount(0);
    await expect(dialog).toBeHidden();
  });

  // With several newer versions the operator must pick one per registry; the plan
  // is stubbed because disagreeing registries cannot be staged headless.
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

    await expect.poll(() => upgradeRuns.length).toBe(1);
    expect(upgradeRuns[0]?.environment).toBe('pick-env');
    expect(upgradeRuns[0]?.version).toBe(newer);
    await expect(dialog).toBeHidden();
  });
});
