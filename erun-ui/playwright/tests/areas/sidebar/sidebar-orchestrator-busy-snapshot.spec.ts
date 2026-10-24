import type { Locator, Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// An orchestrator's sidebar spinner must not depend only on the ai-activity
// Wails event's single false→true transition: anything that loses that one
// event (a fresh mount, a window reopen, a listener that attached a beat late)
// would leave the row reading idle, so orchestratorInfo's list snapshot
// carries the busy flag too.
//
// This spec stubs ListOrchestrators over /__erun_invoke to answer busy without
// ever emitting the ai-activity event, then reboots the app (a real fresh
// mount, not a simulated one) and asserts the row still spins. The event path itself
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

  // Hovering the busy spinner reveals a tooltip, like every other spinner in
  // the sidebar (the shell indicator three lines below it, and every
  // environment row's own activity popover).
  test('hovering the busy spinner reveals a tooltip naming the orchestrator', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, true);
    await app.reboot();

    await app.sidebar.hoverOrchestratorBusySpinner(SEED_ORCHESTRATOR);

    await expect(app.sidebar.orchestratorBusyTooltip(SEED_ORCHESTRATOR)).toBeVisible();
  });

  // hoverOrchestratorBusySpinner freezes `.animate-spin` before it reaches for
  // the spinner, because Playwright's hover "stable" check can never be
  // satisfied by a continuously-resizing box. That freeze is the helper's
  // whole reason to exist, and a helper that quietly stopped performing it
  // would leave the hover above flaking rather than failing with a cause.
  //
  // The direction is what gives the assertion meaning: the spinner really is
  // animating beforehand, so this cannot pass vacuously against a spinner that
  // never animated.
  test('hovering the busy spinner freezes its spin before the hover', async ({ app, page }) => {
    await stubOrchestratorList(page, true);
    await app.reboot();

    const spinner = app.sidebar.orchestratorBusySpinner(SEED_ORCHESTRATOR);
    await expect(spinner).toBeVisible();
    expect(await spinnerAnimationName(spinner)).not.toBe('none');

    await app.sidebar.hoverOrchestratorBusySpinner(SEED_ORCHESTRATOR);

    expect(await spinnerAnimationName(spinner)).toBe('none');
    await expect(app.sidebar.orchestratorBusyTooltip(SEED_ORCHESTRATOR)).toBeVisible();
  });
});

async function spinnerAnimationName(spinner: Locator): Promise<string> {
  return spinner.evaluate((el) => getComputedStyle(el).animationName);
}
