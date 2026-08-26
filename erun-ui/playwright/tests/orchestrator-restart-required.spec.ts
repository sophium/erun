import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR, SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// erun#1319: re-scoping a running orchestrator changes what it is allowed to
// touch, but its live session keeps whatever MCP toolset it was spawned with —
// a Claude Code session resolves --mcp-config once at launch and cannot be
// rewired in place. Before the fix, the desktop listed the orchestrator as
// "running" with its new environment set and said nothing about the mismatch,
// so the operator had no way to learn their edit had not taken effect. The Go
// side of the fix (which environments a session was actually wired for vs
// what it is configured with now) is covered by
// TestUpdateOrchestratorOnALiveSessionLeavesItsToolsetStale in
// erun-ui/orchestrator_test.go, down to the on-disk MCP config; this spec
// covers the rendering and the control the operator gets to resolve it.
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
  test('the sidebar row and hover card say a restart is required, not plain "running"', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();

    // RED against the pre-fix shape: a plain running dot would also match
    // `Orchestrator ${name} is running`, so asserting the more specific label
    // is what tells the two states apart.
    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeVisible();

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();
    const card = page.getByRole('dialog', { name: `${SEED_ORCHESTRATOR} details` });
    await expect(card).toBeVisible();
    await expect(card).toContainText('Running');
    await expect(card).toContainText('Its environments changed while it was running');
  });

  test('a clean running orchestrator shows neither the dot nor the notice', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, runningStaleSnapshot({ restartRequired: false }));
    await app.reboot();

    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    await expect(app.sidebar.orchestratorRestartRequiredDot(SEED_ORCHESTRATOR)).toBeHidden();

    await app.sidebar.orchestratorRowButton(SEED_ORCHESTRATOR).hover();
    const card = page.getByRole('dialog', { name: `${SEED_ORCHESTRATOR} details` });
    await expect(card).toBeVisible();
    await expect(card).not.toContainText('Its environments changed while it was running');
  });

  test('the manage dialog carries the restart control that resolves it', async ({ app, page }) => {
    await stubOrchestratorList(page, runningStaleSnapshot());
    await app.reboot();

    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForOpen('Edit orchestrator');

    await expect(app.orchestratorDialog.restartRequiredNotice()).toBeVisible();
    await expect(app.orchestratorDialog.restartNowButton()).toBeVisible();
    // The footer's own restart action names the remedy too, not just the banner.
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

    // GREEN: the notice's own action reaches the real restart primitive
    // (RestartOrchestrator — stop the stale session, spawn a fresh one, which
    // re-wires wireOrchestratorMCP for the current scope) rather than a
    // second, weaker mechanism. Closing the dialog is the visible confirmation
    // that the action was taken, not left pending behind a modal.
    await expect.poll(() => restartedID).toBe(SEED_ORCHESTRATOR);
    await app.orchestratorDialog.waitForClosed('Edit orchestrator');
  });
});
