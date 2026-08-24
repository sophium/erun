import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../fixtures/seedRoot.js';

// #1230: the review panel used to flatten every MCP-unreachable LoadDiff
// failure into one fixed "Cannot reach the environment runtime / connection
// is down" red alert with a "Reconnect…" action, discarding the distinction
// DescribeLocalMCPUnreachable already computes between a never-opened/stopped
// environment (informational, "Open") and a genuinely stale port-forward (a
// fault, "Reconnect…"). It also rendered that alert twice at once -- once in
// the diff panel, once in the changed-files aside -- for the same env slot.
//
// These specs stub LoadDiff's error message with the two marker prefixes
// erun-ui/mcp_errors.go now emits (mcpUnreachableKindMarkers) rather than
// driving a real unreachable MCP edge, matching the stubbing pattern already
// used by orchestrator-cross-env-diff.spec.ts for this same RPC.

const NOT_OPEN_MESSAGE =
  'ERUN_MCP_UNREACHABLE_NOT_OPEN: mcp unreachable: no port-forward is listening for pw/env on 127.0.0.1:17999';
const STALE_MESSAGE =
  'ERUN_MCP_UNREACHABLE_STALE: mcp unreachable: the port-forward for pw/env on 127.0.0.1:17999 is not carrying traffic (the local port is held but the edge never answers) — re-establishing it';

async function stubLoadDiffError(page: Page, message: string): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method !== 'LoadDiff') {
      await route.continue();
      return;
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
    const review = app.reviewPanel;

    // Not a fault: no role="alert" anywhere, and the informational status
    // names the real situation instead of claiming a connection was lost.
    await expect(review.errorAlerts()).toHaveCount(0);
    const status = review.reachabilityStatuses().filter({ hasText: 'Environment not running' });
    await expect(status).toBeVisible();
    await expect(status.getByRole('button', { name: 'Open' })).toBeVisible();
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
    const review = app.reviewPanel;

    const alert = review.errorAlerts();
    await expect(alert).toHaveCount(1);
    await expect(alert.getByText('Cannot reach the environment runtime')).toBeVisible();
    await expect(alert.getByRole('button', { name: 'Reconnect…' })).toBeVisible();
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
    const review = app.reviewPanel;

    // Both surfaces are visible at once here -- no collapseChangedFilesSection()
    // -- which is exactly the layout that used to render two copies of the
    // same alert (#1230).
    await expect(review.changedFilesTree()).toBeVisible();
    await expect(review.errorAlerts()).toHaveCount(1);
  });

  test('the changed-files aside does not duplicate the informational not-open status either', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubLoadDiffError(page, NOT_OPEN_MESSAGE);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;

    await expect(review.changedFilesTree()).toBeVisible();
    await expect(
      review.reachabilityStatuses().filter({ hasText: 'Environment not running' }),
    ).toHaveCount(1);
  });

  test('clicking Open surfaces honest copy for the stopped case, not the "restore the connection" fault script', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubLoadDiffError(page, NOT_OPEN_MESSAGE);
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;

    await review
      .reachabilityStatuses()
      .filter({ hasText: 'Environment not running' })
      .getByRole('button', { name: 'Open' })
      .click();

    const dialog = page.getByRole('dialog', { name: 'Open environment?' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('This runs `erun open` to start the environment.')).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Open' })).toBeVisible();
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
      directories: ['/tmp/orch-alpha', '/tmp/orch-beta'],
      sessionId: RUNNING_SESSION_ID,
      status: 'running',
      busy: false,
      transient: false,
      shellRunning: false,
      shellCommand: '',
      shellStartedAtUnix: 0,
    };
  }

  // stubOrchestratorNotOpenAlpha reports alpha as stopped (not-open) and beta
  // as reachable, and records every ReconnectMCP call's target so the spec can
  // assert which environment an "Open" click actually reconnects.
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
        // beta must resolve as a genuine success, not fall through to the real
        // backend: the seeded baseline envs are never actually running, so an
        // un-stubbed beta would also report not-open and make "Environment not
        // running" ambiguous between the two sections.
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
    // Opening an orchestrator session clears the sidebar's own selection
    // (selection.selected = null): the pre-#1230 reconnect thunks read their
    // target from that selection, so clicking a linked env's action was a
    // silent no-op whenever it did not match -- which for an orchestrator
    // session was always, since there was no sidebar selection at all.
    const { reconnectCalls } = await stubOrchestratorNotOpenAlpha(page);
    await app.reboot();
    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await app.titlebar.toggleReviewPanel();
    const review = app.reviewPanel;

    await expect(review.envSectionHeader(ALPHA_ENV_KEY)).toBeVisible();
    await expect(review.envSectionHeader(BETA_ENV_KEY)).toBeVisible();

    const alphaStatus = review
      .reachabilityStatuses()
      .filter({ hasText: 'Environment not running' });
    await expect(alphaStatus).toBeVisible();
    await alphaStatus.getByRole('button', { name: 'Open' }).click();

    const dialog = page.getByRole('dialog', { name: 'Open environment?' });
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Open' }).click();

    await expect
      .poll(() => reconnectCalls)
      .toEqual([{ tenant: SEED_TENANT, environment: SEED_ENV_ALPHA }]);
  });
});
