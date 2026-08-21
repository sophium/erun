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
    conversationId?: string;
    resumePrompt: string;
    alsoReopen?: { orchestratorId: string; conversationId?: string }[];
    notice?: string;
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
    if (body.method === 'StartOrchestrator' || body.method === 'StartOrchestratorWithResume') {
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
  // A plain launch must resume the exact conversation the backend
  // resolved as live for this orchestrator, never a blind StartOrchestrator
  // that would leave the CLI to derive (or mis-derive) a session id itself.
  test('a plain launch resumes the recorded conversation, running no prompt', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    await stubReopen(
      page,
      { orchestratorId: SEED_ORCHESTRATOR, conversationId: 'conv-recorded', resumePrompt: '' },
      calls,
    );
    await app.reboot();

    // The restored orchestrator owns the pane: the strip is in orchestrator
    // mode with its tab selected, rather than the selected environment's
    // terminals. Before the fix this only ever happened after the in-app
    // Restart button; a plain launch fell through to the default environment.
    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toHaveAttribute('aria-selected', 'true');
    await expect(app.tabStrip.environmentMode()).toBeHidden();
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible();

    // Resumed explicitly, idle: a plain launch resumes the recorded
    // conversation and hands it no task. The prompt-carrying call belongs to
    // the restart hand-off alone.
    expect(calls).toContain('StartOrchestratorWithResume');
    expect(calls).not.toContain('StartOrchestrator');
  });

  // When the backend has nothing safe to resume (no live session was
  // ever recorded, or the recorded one no longer exists), it answers with no
  // conversationId — and the launch must start the orchestrator fresh rather
  // than resuming whatever the frontend, or the CLI, would otherwise guess.
  test('with no recorded conversation the launch starts the orchestrator fresh', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    await stubReopen(page, { orchestratorId: SEED_ORCHESTRATOR, resumePrompt: '' }, calls);
    await app.reboot();

    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toHaveAttribute('aria-selected', 'true');
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
  // The orchestrator still reopens idle, resuming its own recorded conversation
  // rather than the one the refused hand-off named, and the reason is on screen
  // beside the orchestrator list, where the operator acts on it. Which hand-offs
  // get refused is the Go side's call (erun-ui/app_restart_test.go); what only
  // the rendered app can show is that a refusal is visible and runs no task.
  test('a refused hand-off reopens idle, resuming its own conversation, and says why', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    const notice =
      'Reopened pw-orch without continuing its task: its environments changed (was pw/alpha, now pw/beta). ' +
      'Check RESUME-NOTE.pw-orch.md in the orchestrators working directory before telling it to carry on.';
    await stubReopen(
      page,
      { orchestratorId: SEED_ORCHESTRATOR, conversationId: 'conv-own', resumePrompt: '', notice },
      calls,
    );
    await app.reboot();

    await expect(app.sidebar.orchestratorsAlert()).toContainText(notice);
    expect(calls).toContain('StartOrchestratorWithResume');
    expect(calls).not.toContain('StartOrchestrator');
  });

  // Several orchestrators can restart at once and only one owns the pane, so
  // the ones left mid-task have to be visible somewhere. The Go side decides
  // which hand-off is delivered and which are merely reported
  // (erun-ui/app_restart_test.go); what only the rendered app can show is that
  // the report reaches the operator on a launch that DID resume someone — the
  // case where the notice is easiest to lose, because the app looks correct.
  test('hand-offs the launch could not reopen are reported beside the resumed one', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    const notice =
      'Also restarted mid-task but not reopened: petios (RESUME-NOTE.petios.md). ' +
      'A launch reopens one orchestrator, so start each of these and have it read its return ' +
      'note in the orchestrators working directory.';
    await stubReopen(
      page,
      {
        orchestratorId: SEED_ORCHESTRATOR,
        conversationId: 'conv-1',
        resumePrompt: 'Read RESUME-NOTE.pw-orch.md in this working directory',
        notice,
      },
      calls,
    );
    await app.reboot();

    // The delivered hand-off still resumes with its task...
    await expect(app.tabStrip.orchestratorMode()).toBeVisible();
    expect(calls).toContain('StartOrchestratorWithResume');
    // ...and the orchestrator that was not reopened is named where the operator
    // acts on it, with the note that holds its unfinished work.
    await expect(app.sidebar.orchestratorsAlert()).toContainText('petios');
    await expect(app.sidebar.orchestratorsAlert()).toContainText('RESUME-NOTE.petios.md');
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
      {
        orchestratorId: SEED_ORCHESTRATOR_2,
        conversationId: 'conv-owner',
        resumePrompt: '',
        alsoReopen: [{ orchestratorId: SEED_ORCHESTRATOR, conversationId: 'conv-also' }],
      },
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

    // Only the named pane owner is selected; the other reopened idle, off the
    // pane.
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR_2)).toHaveAttribute('aria-selected', 'true');
    await expect(app.tabStrip.tab(SEED_ORCHESTRATOR)).toHaveAttribute('aria-selected', 'false');

    // Both were resumed explicitly, idle, at their own recorded conversation —
    // the pane owner's and the one alongside it, since a plain launch (no
    // restart hand-off) carries no prompt to auto-run.
    expect(calls).toContain('StartOrchestratorWithResume');
    expect(calls).not.toContain('StartOrchestrator');
  });
});
