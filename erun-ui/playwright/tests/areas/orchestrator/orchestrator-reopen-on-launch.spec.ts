import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// The Go side owns the record of what was open; the backend answer is stubbed
// so the boot decision is exercised on its own.

const RESTORED_SESSION_ID = 4242;
const SEED_ORCHESTRATOR_2 = 'pw-orch-2';

function runningOrchestratorInfo(id: string, sessionId: number) {
  return {
    id,
    name: id,
    environments: [],
    tenants: [],
    directories: [],
    sessionId,
    status: 'running',
    transient: false,
  };
}

const runningOrchestrator = runningOrchestratorInfo(SEED_ORCHESTRATOR, RESTORED_SESSION_ID);

// `running` maps each orchestrator id the launch may start to its session.
async function stubReopen(
  page: Page,
  target: {
    orchestratorId: string;
    alsoReopen?: { orchestratorId: string }[];
  },
  calls: string[],
  running: Record<string, ReturnType<typeof runningOrchestratorInfo>> = {
    [SEED_ORCHESTRATOR]: runningOrchestrator,
  },
): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string; args?: unknown[] };
    calls.push(body.method);
    if (body.method === 'ResolveOrchestratorToReopen') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: target }),
      });
    }
    if (target.orchestratorId === '' && !target.alsoReopen?.length) {
      return route.continue();
    }
    if (body.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: Object.values(running) }),
      });
    }
    // The strip keys off the live session id.
    if (body.method === 'StartOrchestrator') {
      const rawId = body.args?.[0];
      const id = typeof rawId === 'string' ? rawId : '';
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: running[id] ?? runningOrchestrator }),
      });
    }
    await route.continue();
  });
}

test.describe('reopening the orchestrator that was open', () => {
  test('a plain launch reopens the orchestrator that was open on the pane', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    await stubReopen(page, { orchestratorId: SEED_ORCHESTRATOR }, calls);
    await app.reboot();

    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toHaveAttribute('aria-selected', 'true');
    await expect(app.tabStrip.environmentMode()).toBeHidden();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    expect(calls).toContain('StartOrchestrator');
  });

  test('with nothing to reopen the launch starts no orchestrator', async ({ app, page }) => {
    const calls: string[] = [];
    await stubReopen(page, { orchestratorId: '' }, calls);
    // app.open() returns once boot has settled, which bounds the negative
    // assertions below.
    await app.open();

    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();
    await expect(app.tabStrip.orchestratorMode()).toBeHidden();
    expect(calls).toContain('ResolveOrchestratorToReopen');
    expect(calls).not.toContain('StartOrchestrator');
  });
});

test.describe('restoring every orchestrator that was open', () => {
  test('every orchestrator that was open is restored; only the pane owner is selected', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    const owner = runningOrchestratorInfo(SEED_ORCHESTRATOR_2, RESTORED_SESSION_ID + 1);
    await stubReopen(
      page,
      { orchestratorId: SEED_ORCHESTRATOR_2, alsoReopen: [{ orchestratorId: SEED_ORCHESTRATOR }] },
      calls,
      { [SEED_ORCHESTRATOR]: runningOrchestrator, [SEED_ORCHESTRATOR_2]: owner },
    );
    await app.reboot();

    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toBeVisible();
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR_2)).toBeVisible();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR_2, 'running')).toBeVisible();

    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR_2)).toHaveAttribute('aria-selected', 'true');
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toHaveAttribute('aria-selected', 'false');
    expect(calls.filter((method) => method === 'StartOrchestrator')).toHaveLength(2);
  });
});
