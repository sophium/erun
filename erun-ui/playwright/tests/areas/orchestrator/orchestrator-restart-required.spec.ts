import type { Page } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR, SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// A live session's MCP toolset is fixed at launch, so re-scoping a running
// orchestrator must surface a restart.
const RUNNING_SESSION_ID = 7373;

function runningStaleSnapshot(overrides: Record<string, unknown> = {}) {
  return {
    id: SEED_ORCHESTRATOR,
    name: SEED_ORCHESTRATOR,
    environments: [{ tenant: SEED_TENANT, environment: SEED_ENV_ALPHA, directory: '/tmp/a' }],
    tenants: [SEED_TENANT],
    directories: ['/tmp/a'],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    nudgeCapped: false,
    restartRequired: true,
    ...overrides,
  };
}

async function stubOrchestratorList(page: Page, body: unknown): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (parsed.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [body] }),
      });
    }
    await route.continue();
  });
}

test.describe('a running orchestrator whose scope changed under it (erun#1319)', () => {
  // A hover card closed under a stationary pointer stays closed, so a second read
  // against it must re-hover; readOrchestratorHoverCard wraps that retry. Reads
  // carry a short bound so a missed attempt fails fast enough to retry.
  test('a hover card dropped between two reads is re-hovered, not read as absent', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();
    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeVisible();

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toContainText('Running', { timeout: 1_000 });
    });

    // Escape, not a focus move: focus leaving a row that holds the card is
    // refused, while an explicit dismissal still closes it.
    await page.keyboard.press('Escape');
    await expect(app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR)).toBeHidden(withTestBudget());

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toContainText('Running', { timeout: 1_000 });
      await expect(card).toContainText('Its environments changed while it was running', {
        timeout: 1_000,
      });
    });
  });

  test('the sidebar row and hover card say a restart is required, not plain "running"', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();

    // The plain running dot's label is a prefix of this one.
    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeVisible();

    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toContainText('Running', { timeout: 1_000 });
      await expect(card).toContainText('Its environments changed while it was running', {
        timeout: 1_000,
      });
    });
  });

  test('a clean running orchestrator shows neither the dot nor the notice', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, runningStaleSnapshot({ restartRequired: false }));
    await app.reboot();

    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeHidden();

    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    const card = page.getByRole('dialog', { name: `${SEED_ORCHESTRATOR} details` });
    await expect(card).toBeVisible();
    await expect(card).not.toContainText('Its environments changed while it was running');
  });

  // The restart-required label starts with the plain running label, and
  // role-name queries match substrings.
  test('a restart-required row does not read as a plain running dot', async ({ app, page }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();

    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeVisible();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeHidden();
  });

  test('the manage dialog carries the restart control that resolves it', async ({ app, page }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();

    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');

    await expect(app.orchestratorDialog.restartRequiredNotice()).toBeVisible();
    await expect(app.orchestratorDialog.restartNowButton()).toBeVisible();
    await expect(app.orchestratorDialog.footerRestartButton()).toHaveText('Restart to apply');

    let restartedID = '';
    await page.route('**/__erun_invoke', async (route, request) => {
      const parsed = JSON.parse(request.postData() ?? '{}') as {
        method?: string;
        args?: [string?];
      };
      if (parsed.method === 'RestartOrchestrator') {
        restartedID = parsed.args?.[0] ?? '';
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: runningStaleSnapshot({
              sessionId: RUNNING_SESSION_ID + 1,
              restartRequired: false,
            }),
          }),
        });
      }
      await route.continue();
    });

    await app.orchestratorDialog.restartNowButton().click();

    await expect.poll(() => restartedID).toBe(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });
});
