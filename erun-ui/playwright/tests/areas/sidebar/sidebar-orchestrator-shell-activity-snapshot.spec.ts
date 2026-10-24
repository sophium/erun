import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// The shell indicator must render from the ListOrchestrators snapshot alone,
// without the orchestrator-shell-activity event; the event path is Go-tested.

const RUNNING_SESSION_ID = 4242;
const SHELL_COMMAND = 'sleep 300';
const SHELL_STARTED_AT_UNIX = 1_700_000_000;

function orchestratorSnapshot(shellRunning: boolean, busy = false) {
  return {
    id: SEED_ORCHESTRATOR,
    name: SEED_ORCHESTRATOR,
    environments: [],
    tenants: [],
    directories: [],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy,
    transient: false,
    shellRunning,
    shellCommand: shellRunning ? SHELL_COMMAND : '',
    shellStartedAtUnix: shellRunning ? SHELL_STARTED_AT_UNIX : 0,
  };
}

async function stubOrchestratorList(
  page: Page,
  shellRunning: boolean,
  busy = false,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [orchestratorSnapshot(shellRunning, busy)] }),
      });
    }
    await route.continue();
  });
}

test.describe('orchestrator background shell renders from the list snapshot', () => {
  test('a running-shell snapshot shows the indicator on a fresh mount, with no event ever emitted', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, true);
    await app.reboot();

    await expect(app.sidebar.orchestratorShellSpinner(SEED_ORCHESTRATOR)).toBeVisible();
    await expect(app.sidebar.orchestratorShellSpinner(SEED_ORCHESTRATOR)).toHaveAccessibleName(
      new RegExp(SHELL_COMMAND),
    );
  });

  test('no running shell renders no indicator', async ({ app, page }) => {
    await stubOrchestratorList(page, false);
    await app.reboot();

    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    await expect(app.sidebar.orchestratorShellSpinner(SEED_ORCHESTRATOR)).toHaveCount(0);
  });

  // The row shows one motion cue at a time: a busy turn hides the shell indicator.
  test('an actively busy turn shows only the busy spinner, not the shell indicator too', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, true, true);
    await app.reboot();

    await expect(app.sidebar.orchestratorBusySpinner(SEED_ORCHESTRATOR)).toBeVisible();
    await expect(app.sidebar.orchestratorShellSpinner(SEED_ORCHESTRATOR)).toHaveCount(0);
  });
});
