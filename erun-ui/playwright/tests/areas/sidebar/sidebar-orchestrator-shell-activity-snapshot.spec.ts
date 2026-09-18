import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// A running background shell had no activity affordance at all — the
// desktop rendered it as static text, so working and wedged looked identical.
// The fix follows the exact busy-snapshot pattern this mirrors: orchestratorInfo
// carries shellRunning/shellCommand/shellStartedAtUnix directly, so a snapshot
// taken after the shell started reflects it even when the
// orchestrator-shell-activity event that announced it was never observed —
// and it is independent of the turn's own busy state, since a background
// shell can keep running after the turn that started it goes idle.
//
// This spec drives the fix's frontend half directly: it stubs
// ListOrchestrators over /__erun_invoke to answer a running shell without
// ever emitting the orchestrator-shell-activity event, then reboots the app
// (a real fresh mount) and asserts the indicator still shows. The event path
// itself and the backend's own re-emit timing are covered by the Go tests in
// erun-ui/session_heartbeat_test.go
// (TestReconcileOrchestratorActivityReEmitsShellStateEveryTick,
// TestOrchestratorShellSnapshotRendersRunningWithoutTheEvent), since the
// headless harness cannot wait out a real 15s poll deterministically.

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
    // The whole point: not just a bare spinner, but what is running.
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

  // The turn's own busy spinner and the shell indicator are independent
  // facts, but the row shows only one motion cue at a time: a turn that is
  // actively busy already spins, so the shell indicator stays hidden rather
  // than doubling up.
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
