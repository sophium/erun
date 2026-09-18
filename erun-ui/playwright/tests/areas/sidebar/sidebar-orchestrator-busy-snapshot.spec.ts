import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// #1087: an orchestrator's sidebar spinner used to be lit only by the
// ai-activity Wails event, at the single false→true transition — and
// orchestratorInfo, the list snapshot the frontend boots and re-renders from,
// carried no busy flag at all. Anything that lost that one event (a fresh
// mount, a window reopen, a listener that attached a beat late) left the row
// reading idle for however long the rest of the turn ran.
//
// This spec drives the fix's frontend half directly: it stubs ListOrchestrators
// over /__erun_invoke to answer busy without ever emitting the ai-activity
// event, then reboots the app (a real fresh mount, not a simulated one) and
// asserts the row still spins. Before the fix this would read idle, because
// nothing but that missing event could have set it. The event path itself
// (ai-activity flipping the spinner while the app is already running) is
// already covered by sidebar-ai-activity.spec.ts; the backend's own re-emit
// timing is covered by the Go tests in erun-ui/session_heartbeat_test.go
// (TestReconcileOrchestratorActivityReEmitsEveryTick,
// TestOrchestratorSnapshotRendersBusyWithoutTheEvent) since the headless
// harness cannot wait out a real 15s poll deterministically.

const RUNNING_SESSION_ID = 4242;

function orchestratorSnapshot(busy: boolean) {
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
  };
}

async function stubOrchestratorList(page: Page, busy: boolean): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [orchestratorSnapshot(busy)] }),
      });
    }
    await route.continue();
  });
}

test.describe('orchestrator busy renders from the list snapshot (#1087)', () => {
  test('a busy snapshot spins the row on a fresh mount, with no ai-activity event ever emitted', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, true);
    await app.reboot();

    await expect(app.sidebar.orchestratorBusySpinner(SEED_ORCHESTRATOR)).toBeVisible();
  });

  test('a non-busy snapshot renders no spinner', async ({ app, page }) => {
    await stubOrchestratorList(page, false);
    await app.reboot();

    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    await expect(app.sidebar.orchestratorBusySpinner(SEED_ORCHESTRATOR)).toHaveCount(0);
  });

  // Hovering the busy spinner used to reveal nothing, unlike every other
  // spinner in the sidebar (the shell indicator three lines below it, and
  // every environment row's own activity popover).
  test('hovering the busy spinner reveals a tooltip naming the orchestrator', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, true);
    await app.reboot();

    await app.sidebar.hoverOrchestratorBusySpinner(SEED_ORCHESTRATOR);

    await expect(app.sidebar.orchestratorBusyTooltip(SEED_ORCHESTRATOR)).toBeVisible();
  });
});
