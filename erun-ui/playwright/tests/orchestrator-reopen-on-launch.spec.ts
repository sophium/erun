import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../fixtures/seedRoot.js';

// The desktop reopens the orchestrator that was open when it last ran. The Go
// side owns the durable record of what that orchestrator is
// (erun-ui/orchestrator_open_state_test.go covers a plain launch, an explicitly
// stopped orchestrator, and the one-shot restart hand-off); what only the
// rendered app can show is what boot does with the answer — whether the
// orchestrator ends up owning the terminal pane instead of the default
// environment selection, and which start call the launch makes. The backend
// answer is stubbed over /__erun_invoke so the boot decision is exercised
// without a real Claude process, which the inert harness deliberately lacks.

const RESTORED_SESSION_ID = 4242;

const runningOrchestrator = {
  id: SEED_ORCHESTRATOR,
  name: SEED_ORCHESTRATOR,
  environments: [],
  tenants: [],
  directories: [],
  sessionId: RESTORED_SESSION_ID,
  status: 'running',
  transient: false,
};

// stubReopen answers the boot restore round-trip and records every method the
// frontend invoked, so a spec can assert which start call the launch made — and
// that it made none at all when there is nothing to reopen.
async function stubReopen(
  page: Page,
  target: {
    orchestratorId: string;
    conversationId?: string;
    resumePrompt: string;
    notice?: string;
  },
  calls: string[],
): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    calls.push(body.method);
    if (body.method === 'ResolveOrchestratorToReopen') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: target }),
      });
    }
    if (target.orchestratorId === '') {
      return route.continue();
    }
    // Once a restore is expected, the orchestrator has to read as running for
    // the pane to be its own — the strip keys off the live session id.
    if (
      body.method === 'ListOrchestrators' ||
      body.method === 'StartOrchestrator' ||
      body.method === 'StartOrchestratorWithResume'
    ) {
      const data =
        body.method === 'ListOrchestrators' ? [runningOrchestrator] : runningOrchestrator;
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data }),
      });
    }
    await route.continue();
  });
}

test.describe('reopening the orchestrator that was open', () => {
  test('a plain launch lands in the orchestrator, running no prompt', async ({ app, page }) => {
    const calls: string[] = [];
    await stubReopen(page, { orchestratorId: SEED_ORCHESTRATOR, resumePrompt: '' }, calls);
    await app.reboot();

    // The restored orchestrator owns the pane: the strip is in orchestrator
    // mode with its tab selected, rather than the selected environment's
    // terminals. Before the fix this only ever happened after the in-app
    // Restart button; a plain launch fell through to the default environment.
    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toHaveAttribute('aria-selected', 'true');
    await expect(app.tabStrip.environmentMode()).toBeHidden();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();

    // Idle at the prompt: a plain launch resumes the conversation and hands it
    // no task. The prompt-carrying call belongs to the restart hand-off alone.
    expect(calls).toContain('StartOrchestrator');
    expect(calls).not.toContain('StartOrchestratorWithResume');
  });

  test('a restart hand-off resumes with its prompt', async ({ app, page }) => {
    const calls: string[] = [];
    await stubReopen(
      page,
      {
        orchestratorId: SEED_ORCHESTRATOR,
        conversationId: 'conv-1',
        resumePrompt: 'finish the task',
      },
      calls,
    );
    await app.reboot();

    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    expect(calls).toContain('StartOrchestratorWithResume');
    expect(calls).not.toContain('StartOrchestrator');
  });

  // A hand-off the backend refuses — the orchestrator was re-scoped, or the
  // conversation that asked can no longer be identified — must never be silent.
  // The orchestrator still reopens, idle, and the reason is on screen beside the
  // orchestrator list, where the operator acts on it. Which hand-offs get refused
  // is the Go side's call (erun-ui/app_restart_test.go); what only the rendered
  // app can show is that a refusal is visible and runs no task.
  test('a refused hand-off reopens idle and says why', async ({ app, page }) => {
    const calls: string[] = [];
    const notice =
      'Reopened pw-orch without continuing its task: its environments changed (was pw/alpha, now pw/beta).';
    await stubReopen(page, { orchestratorId: SEED_ORCHESTRATOR, resumePrompt: '', notice }, calls);
    await app.reboot();

    await expect(app.sidebar.orchestratorsAlert()).toContainText(notice);
    expect(calls).toContain('StartOrchestrator');
    expect(calls).not.toContain('StartOrchestratorWithResume');
  });

  test('with nothing to reopen the launch starts no orchestrator', async ({ app, page }) => {
    const calls: string[] = [];
    await stubReopen(page, { orchestratorId: '', resumePrompt: '' }, calls);
    // app.open() returns only once boot has settled (the loading overlay is
    // gone and the sidebar has rendered), so the negative assertions below are
    // bounded by a real event rather than a guessed delay.
    await app.open();

    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'stopped')).toBeVisible();
    await expect(app.tabStrip.orchestratorMode()).toBeHidden();
    expect(calls).toContain('ResolveOrchestratorToReopen');
    expect(calls).not.toContain('StartOrchestrator');
    expect(calls).not.toContain('StartOrchestratorWithResume');
  });
});
