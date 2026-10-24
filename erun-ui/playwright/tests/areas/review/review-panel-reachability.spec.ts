import type { Page } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The panel distinguishes a not-running env (informational, "Open") from a stale port-forward (a
// fault, "Reconnect…"), rendered once per env slot.
// LoadDiff errors are stubbed with the marker prefixes from erun-ui/mcp_errors.go.

const NOT_OPEN_MESSAGE =
  'ERUN_MCP_UNREACHABLE_NOT_OPEN: mcp unreachable: no port-forward is listening for pw/env on 127.0.0.1:17999';
const STALE_MESSAGE =
  'ERUN_MCP_UNREACHABLE_STALE: mcp unreachable: the port-forward for pw/env on 127.0.0.1:17999 is not carrying traffic (the local port is held but the edge never answers) — re-establishing it';

// holdMs delays only the first LoadDiff, timed from that request's arrival.
async function stubLoadDiffError(page: Page, message: string, holdMs = 0): Promise<void> {
  let held = false;
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method !== 'LoadDiff') {
      await route.continue();
      return;
    }
    if (holdMs && !held) {
      held = true;
      await new Promise((resolve) => setTimeout(resolve, holdMs));
    }
    return route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ error: message }),
    });
  });
}

test.describe('review panel reachability framing (#1230)', () => {
  test('a stopped/never-opened environment renders informationally, with Open as the action', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubLoadDiffError(page, NOT_OPEN_MESSAGE);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;

    // Converge on the status before reading absences; an absence read first would pass on an
    // unrendered panel.
    const status = review.reachabilityStatuses().filter({ hasText: 'Environment not running' });
    await expect(status).toBeVisible(withTestBudget());
    await expect(review.errorAlerts()).toHaveCount(0);
    await expect(status.getByRole('button', { name: 'Open' })).toBeVisible(withTestBudget());
    await expect(status.getByRole('button', { name: 'Reconnect…' })).toHaveCount(0);
  });

  test('a stale port-forward renders as an actionable fault with Reconnect…', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubLoadDiffError(page, STALE_MESSAGE);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;

    const alert = review.errorAlerts();
    await expect(alert).toHaveCount(1, withTestBudget());
    await expect(alert.getByText('Cannot reach the environment runtime')).toBeVisible(
      withTestBudget(),
    );
    await expect(alert.getByRole('button', { name: 'Reconnect…' })).toBeVisible(withTestBudget());
    await expect(alert.getByRole('button', { name: 'Open' })).toHaveCount(0);
  });

  test("the changed-files aside does not duplicate the diff panel's alert for the same environment", async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubLoadDiffError(page, STALE_MESSAGE);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;

    // Both surfaces visible at once, where a duplicate alert would show.
    await expect(review.changedFilesTree()).toBeVisible(withTestBudget());
    await expect(review.errorAlerts()).toHaveCount(1, withTestBudget());
  });

  test('the changed-files aside does not duplicate the informational not-open status either', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubLoadDiffError(page, NOT_OPEN_MESSAGE);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;

    await expect(review.changedFilesTree()).toBeVisible(withTestBudget());
    await expect(
      review.reachabilityStatuses().filter({ hasText: 'Environment not running' }),
    ).toHaveCount(1, withTestBudget());
  });

  test('clicking Open surfaces honest copy for the stopped case, not the "restore the connection" fault script', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubLoadDiffError(page, NOT_OPEN_MESSAGE);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;

    await review
      .reachabilityStatuses()
      .filter({ hasText: 'Environment not running' })
      .getByRole('button', { name: 'Open' })
      .click();

    const dialog = page.getByRole('dialog', { name: 'Open environment?' });
    await expect(dialog).toBeVisible(withTestBudget());
    await expect(dialog.getByText('This runs `erun open` to start the environment.')).toBeVisible(
      withTestBudget(),
    );
    await expect(dialog.getByRole('button', { name: 'Open' })).toBeVisible(withTestBudget());
  });

  test('clicking Reconnect… surfaces honest copy for the fault case, not a redeployment the reconnect cannot perform', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubLoadDiffError(page, STALE_MESSAGE);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;

    await review.errorAlerts().getByRole('button', { name: 'Reconnect…' }).click();

    // The dialog must not promise a redeploy: `erun open --reconnect` refuses a stopped env and
    // never deploys.
    const dialog = page.getByRole('dialog', { name: 'Reconnect to environment?' });
    await expect(dialog).toBeVisible(withTestBudget());
    await expect(dialog).not.toContainText('it will be redeployed');
    await expect(dialog).toContainText('It reattaches to the environment runtime');
    await expect(dialog.getByRole('button', { name: 'Reconnect', exact: true })).toBeVisible(
      withTestBudget(),
    );
  });
});

test.describe('review panel reconnect targeting in an orchestrator session (#1230)', () => {
  const ORCHESTRATOR_ID = 'pw-orch-reachability';
  const RUNNING_SESSION_ID = 9101;
  const ALPHA_ENV_KEY = `${SEED_TENANT}/${SEED_ENV_ALPHA}`;
  const BETA_ENV_KEY = `${SEED_TENANT}/${SEED_ENV_BETA}`;

  function orchestratorSnapshot(): unknown {
    return {
      id: ORCHESTRATOR_ID,
      name: ORCHESTRATOR_ID,
      environments: [
        { tenant: SEED_TENANT, environment: SEED_ENV_ALPHA, directory: '/tmp/orch-alpha' },
        { tenant: SEED_TENANT, environment: SEED_ENV_BETA, directory: '/tmp/orch-beta' },
      ],
      tenants: [SEED_TENANT],
      // A fictional path would add a failing section per orchestrator directory.
      directories: [],
      sessionId: RUNNING_SESSION_ID,
      status: 'running',
      busy: false,
      transient: false,
      shellRunning: false,
      shellCommand: '',
      shellStartedAtUnix: 0,
    };
  }

  // Reports alpha as not-open and beta as reachable; records ReconnectMCP targets.
  async function stubOrchestratorNotOpenAlpha(page: Page): Promise<{ reconnectCalls: unknown[] }> {
    const reconnectCalls: unknown[] = [];
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method?: string; args?: unknown[] };
      if (body.method === 'ListOrchestrators') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: [orchestratorSnapshot()] }),
        });
      }
      if (body.method === 'LoadDiff') {
        const [selection] = (body.args ?? []) as [{ environment: string }];
        if (selection.environment === SEED_ENV_ALPHA) {
          return route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({ error: NOT_OPEN_MESSAGE }),
          });
        }
        // An un-stubbed beta would also report not-open and make the status ambiguous.
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              rawDiff: '',
              workingDirectory: '/seed',
              summary: { fileCount: 1, additions: 1, deletions: 0 },
              files: [
                {
                  path: 'beta.ts',
                  status: 'modified',
                  additions: 1,
                  deletions: 0,
                  binary: false,
                  hunks: [],
                },
              ],
              tree: [{ name: 'beta.ts', path: 'beta.ts', type: 'file', depth: 0 }],
              scope: 'current',
              includesWorktree: true,
            },
          }),
        });
      }
      if (body.method === 'ReconnectMCP') {
        reconnectCalls.push(body.args?.[0]);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: null }),
        });
      }
      await route.continue();
    });
    return { reconnectCalls };
  }

  test('opening the linked (unselected) environment targets that environment, not a no-op on the sidebar selection', async ({
    app,
    page,
  }) => {
    // Opening an orchestrator session clears the sidebar selection, so reconnect must not read its
    // target from it.
    const { reconnectCalls } = await stubOrchestratorNotOpenAlpha(page);
    await app.reboot();
    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();
    const review = app.reviewPanel;

    await expect(review.envSectionHeader(ALPHA_ENV_KEY)).toBeVisible(withTestBudget());
    await expect(review.envSectionHeader(BETA_ENV_KEY)).toBeVisible(withTestBudget());

    const alphaStatus = review
      .reachabilityStatuses()
      .filter({ hasText: 'Environment not running' });
    await expect(alphaStatus).toBeVisible(withTestBudget());
    await alphaStatus.getByRole('button', { name: 'Open' }).click();

    const dialog = page.getByRole('dialog', { name: 'Open environment?' });
    await expect(dialog).toBeVisible(withTestBudget());
    await dialog.getByRole('button', { name: 'Open' }).click();

    await expect
      .poll(() => reconnectCalls, withTestBudget())
      .toEqual([{ tenant: SEED_TENANT, environment: SEED_ENV_ALPHA }]);
  });

  // Holds LoadDiff just past expect's 10s default to prove the read waits on the test budget, not
  // the default.
  test('a reachability status that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Covers the 12s hold above.
    test.setTimeout(60_000);
    await stubLoadDiffError(page, NOT_OPEN_MESSAGE, 12_000);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    await app.reviewPanel.waitForOpen();

    const status = app.reviewPanel
      .reachabilityStatuses()
      .filter({ hasText: 'Environment not running' });
    await expect(status).toBeVisible(withTestBudget());
    await expect(status.getByRole('button', { name: 'Open' })).toBeVisible(withTestBudget());
  });
});
