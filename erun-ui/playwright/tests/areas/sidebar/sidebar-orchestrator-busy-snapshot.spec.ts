import type { Locator, Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// The orchestrator list snapshot carries the busy flag, so the spinner survives a missed
// ai-activity event.
// Re-emit timing is covered by erun-ui/session_heartbeat_test.go.

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

  test('hovering the busy spinner reveals a tooltip naming the orchestrator', async ({
    app,
    page,
  }) => {
    await stubOrchestratorList(page, true);
    await app.reboot();

    await app.sidebar.hoverOrchestratorBusySpinner(SEED_ORCHESTRATOR);

    await expect(app.sidebar.orchestratorBusyTooltip(SEED_ORCHESTRATOR)).toBeVisible();
  });

  // Playwright's hover stability check can't settle on a spinning box, so the helper must freeze
  // it; asserts it was animating first.
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
