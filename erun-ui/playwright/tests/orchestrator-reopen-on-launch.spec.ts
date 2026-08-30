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

// A raw notice entry the way the backend's JSON actually shapes one (see
// orchestratorNotice, erun-ui/app_restart.go) -- kind and text untyped as
// `string` here rather than the narrowed union the frontend normalizes to, so
// a spec can also stage a kind the frontend does not recognise.
interface StubNotice {
  orchestratorId?: string;
  kind?: string;
  text: string;
}

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
    notices?: StubNotice[];
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
      {
        orchestratorId: SEED_ORCHESTRATOR,
        conversationId: 'conv-own',
        resumePrompt: '',
        notices: [{ orchestratorId: SEED_ORCHESTRATOR, kind: 'warning', text: notice }],
      },
      calls,
    );
    await app.reboot();

    // A refusal is a warning: it renders through the destructive role="alert"
    // treatment, never the muted role="status" a routine resume gets.
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
        // This notice names several orchestrators at once, so the backend
        // attributes it to none in particular (erun-ui/app_restart.go's
        // orchestratorHandoffsNotReopenedNotice).
        notices: [{ kind: 'warning', text: notice }],
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

// erun#1695: the kind orchestratorConversationNoticeKind computes on the Go
// side (erun-ui/orchestrator_live_conversation.go) used to be discarded at the
// frontend boundary, and every restore notice rendered through one destructive
// role="alert" paragraph regardless of what it actually reported — so a
// resumed tracked conversation (the mechanism working) read identically to a
// genuine refusal. These specs assert the rendered role per kind, which prose
// cannot substitute for.
test.describe('orchestrator restore notices render by kind', () => {
  test('a single info notice renders as a status, not a destructive alert', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    const text =
      'Reopened pw-orch on the conversation its session was last live on (conv-live), ' +
      'not the one derived from its id (conv-derived). Manage the orchestrator to see every ' +
      'conversation it can resume and attach a different one.';
    await stubReopen(
      page,
      {
        orchestratorId: SEED_ORCHESTRATOR,
        conversationId: 'conv-live',
        resumePrompt: '',
        notices: [{ orchestratorId: SEED_ORCHESTRATOR, kind: 'info', text }],
      },
      calls,
    );
    await app.reboot();

    // role="status" only — never role="alert", the treatment reserved for an
    // actual fault.
    await expect(app.sidebar.orchestratorRestoreStatusNotices()).toContainText(text);
    await expect(app.sidebar.orchestratorsAlert()).toHaveCount(0);

    await page.screenshot({ path: 'test-results/orchestrator-restore-notice-info-light.png' });
    await app.titlebar.toggleTheme();
    await expect(app.documentElement()).toHaveClass(/dark/);
    await page.screenshot({ path: 'test-results/orchestrator-restore-notice-info-dark.png' });
  });

  test('a single warning notice keeps the destructive alert treatment it deserves', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    const text =
      'Reopened pw-orch without continuing its task: the conversation that asked for it no longer exists. ' +
      'Check RESUME-NOTE.pw-orch.md in the orchestrators working directory before telling it to carry on.';
    await stubReopen(
      page,
      {
        orchestratorId: SEED_ORCHESTRATOR,
        conversationId: 'conv-own',
        resumePrompt: '',
        notices: [{ orchestratorId: SEED_ORCHESTRATOR, kind: 'warning', text }],
      },
      calls,
    );
    await app.reboot();

    await expect(app.sidebar.orchestratorsAlert()).toContainText(text);
    await expect(app.sidebar.orchestratorRestoreStatusNotices()).toHaveCount(0);

    await page.screenshot({ path: 'test-results/orchestrator-restore-notice-warning-light.png' });
    await app.titlebar.toggleTheme();
    await expect(app.documentElement()).toHaveClass(/dark/);
    await page.screenshot({ path: 'test-results/orchestrator-restore-notice-warning-dark.png' });
  });

  // The heart of the bug: several orchestrators resolved on the same restore
  // must each keep their own kind. Before the fix, this exact case (a genuine
  // warning sitting among routine successes) rendered as one indistinguishable
  // red paragraph — the warning was as easy to miss as it was to mistake a
  // success for a fault.
  test('a mixed batch renders the warning distinctly from the info notices beside it', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    const infoText =
      'Reopened pw-orch-2 on the conversation its session was last live on (conv-owner-live), ' +
      'not the one derived from its id (conv-owner-derived).';
    const warningText =
      'Reopened pw-orch: its environments changed since its last session (was pw/alpha, now pw/beta). ' +
      'Its conversation may hold context for environments it is no longer wired to; ' +
      'check RESUME-NOTE.pw-orch.md before treating it as current.';
    const owner = runningOrchestratorInfo(SEED_ORCHESTRATOR_2, RESTORED_SESSION_ID + 2);
    await stubReopen(
      page,
      {
        orchestratorId: SEED_ORCHESTRATOR_2,
        conversationId: 'conv-owner-live',
        resumePrompt: '',
        alsoReopen: [{ orchestratorId: SEED_ORCHESTRATOR, conversationId: 'conv-also' }],
        notices: [
          { orchestratorId: SEED_ORCHESTRATOR, kind: 'warning', text: warningText },
          { orchestratorId: SEED_ORCHESTRATOR_2, kind: 'info', text: infoText },
        ],
      },
      calls,
      { [SEED_ORCHESTRATOR]: runningOrchestrator, [SEED_ORCHESTRATOR_2]: owner },
    );
    await app.reboot();

    // Two distinct list items, not one joined paragraph...
    await expect(app.sidebar.orchestratorRestoreNotices().locator('li')).toHaveCount(2);
    // ...each at its own role: the warning is the only one that alerts...
    await expect(app.sidebar.orchestratorsAlert()).toHaveCount(1);
    await expect(app.sidebar.orchestratorsAlert()).toContainText(warningText);
    await expect(app.sidebar.orchestratorsAlert()).not.toContainText(infoText);
    // ...and the info notice is the only one that merely reports status.
    await expect(app.sidebar.orchestratorRestoreStatusNotices()).toHaveCount(1);
    await expect(app.sidebar.orchestratorRestoreStatusNotices()).toContainText(infoText);
    await expect(app.sidebar.orchestratorRestoreStatusNotices()).not.toContainText(warningText);

    await page.screenshot({ path: 'test-results/orchestrator-restore-notice-mixed-light.png' });
    await app.titlebar.toggleTheme();
    await expect(app.documentElement()).toHaveClass(/dark/);
    await page.screenshot({ path: 'test-results/orchestrator-restore-notice-mixed-dark.png' });
  });

  // A kind this launch does not recognise — a payload from a backend version
  // that classified notices differently, or one missing the field — must not
  // silently become 'info' (hiding a real problem behind a routine-looking
  // line) or 'warning' (crying wolf over what might be entirely routine). It
  // renders as its own visibly distinct case: role="status" like info (never
  // interrupts like an alert would), but with its own icon and border so it
  // reads as neither of the two known kinds.
  test('an unrecognised kind renders as its own distinct case, never silently as info or a fault', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];
    const text = 'a notice this launch does not know how to classify';
    await stubReopen(
      page,
      {
        orchestratorId: SEED_ORCHESTRATOR,
        conversationId: 'conv-own',
        resumePrompt: '',
        notices: [{ orchestratorId: SEED_ORCHESTRATOR, kind: 'mystery-kind', text }],
      },
      calls,
    );
    await app.reboot();

    await expect(app.sidebar.orchestratorRestoreStatusNotices()).toContainText(text);
    await expect(app.sidebar.orchestratorsAlert()).toHaveCount(0);
  });
});
