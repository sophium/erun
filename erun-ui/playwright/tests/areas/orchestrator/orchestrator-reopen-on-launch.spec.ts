import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// The desktop reopens the orchestrators that were open when it last ran, each
// reattached to its own background agent. The Go side owns the durable record
// of what was open (erun-ui/orchestrator_open_state_test.go covers a plain
// launch, an explicitly stopped orchestrator, and the one-shot restart
// hand-off); what only the rendered app can show is what boot does with the
// answer — whether the orchestrator ends up owning the terminal pane instead of
// the default environment selection, and that every one of them is started.
// The backend answer is stubbed over /__erun_invoke so the boot decision is
// exercised on its own.

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

// stubReopen answers the boot restore round-trip and records every method the
// frontend invoked, so a spec can assert which start call the launch made — and
// that it made none at all when there is nothing to reopen. `running` maps every
// orchestrator id the launch may start to the session it comes back with, so a
// multi-orchestrator restore can give each one its own answer.
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
    // Once a restore is expected, the orchestrator has to read as running for
    // the pane to be its own — the strip keys off the live session id.
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

    // The restored orchestrator owns the pane: the strip is in orchestrator
    // mode with its tab selected, rather than the selected environment's
    // terminals.
    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toHaveAttribute('aria-selected', 'true');
    await expect(app.tabStrip.environmentMode()).toBeHidden();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    expect(calls).toContain('StartOrchestrator');
  });

  test('with nothing to reopen the launch starts no orchestrator', async ({ app, page }) => {
    const calls: string[] = [];
    await stubReopen(page, { orchestratorId: '' }, calls);
    // app.open() returns only once boot has settled (the loading overlay is
    // gone and the sidebar has rendered), so the negative assertions below are
    // bounded by a real event rather than a guessed delay.
    await app.open();

    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();
    await expect(app.tabStrip.orchestratorMode()).toBeHidden();
    expect(calls).toContain('ResolveOrchestratorToReopen');
    expect(calls).not.toContain('StartOrchestrator');
  });
});

// The durable record used to be a single id, so starting a second orchestrator
// silently discarded the record that the first was open, and a launch restored
// exactly one — every other tab came back with no session behind it. The Go
// side owns the durable set and the recency rule that picks a pane owner when
// there is no restart hand-off (erun-ui/orchestrator_open_state_test.go); what
// only the rendered app can show is that every id the backend names actually
// gets a live session and that exactly one of them ends up owning the pane.
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

    // Both orchestrators are live sessions, not just tabs — the tab strip and
    // the sidebar's running dots have to agree with each other.
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toBeVisible();
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR_2)).toBeVisible();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR_2, 'running')).toBeVisible();

    // Only the named pane owner is selected; the other reopened off the pane.
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR_2)).toHaveAttribute('aria-selected', 'true');
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toHaveAttribute('aria-selected', 'false');
    expect(calls.filter((method) => method === 'StartOrchestrator')).toHaveLength(2);
  });
});
