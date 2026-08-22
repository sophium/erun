import assert from 'node:assert/strict';
import { test } from 'node:test';

import { planOrchestratorRestore, readRestoreNotice } from './orchestratorRestore';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';

function orchestrator(id: string, transient = false): OrchestratorInfo {
  return {
    id,
    name: id,
    environments: [],
    tenants: [],
    directories: [],
    sessionId: 0,
    status: 'stopped',
    busy: false,
    transient,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
  };
}

// ref builds an alsoReopen entry the way the backend does: an id plus the
// conversation it resolved for that orchestrator (see resolveReopenSessionID
// in app_restart.go), never left for the frontend to derive.
function ref(orchestratorId: string, conversationId = '') {
  return { orchestratorId, conversationId };
}

const persisted = [orchestrator('agent-1'), orchestrator('agent-2')];

// The defect: a plain quit-and-relaunch had no target at all, so boot fell
// through to the default environment selection. The durable record now answers,
// and it answers without a prompt — the launch resumes idle.
test('a plain launch reopens the orchestrator that was open, running nothing', () => {
  const outcome = planOrchestratorRestore(
    { orchestratorId: 'agent-1', conversationId: 'conv-1', resumePrompt: '' },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-1', conversationId: 'conv-1', resumePrompt: '' },
    alsoReopen: [],
  });
});

// The bug an earlier fix addressed: the durable record was a scalar, so only
// one orchestrator ever came back. Every id in alsoReopen must be restored too.
test('every orchestrator that was open is restored, not just the pane owner', () => {
  const outcome = planOrchestratorRestore(
    {
      orchestratorId: 'agent-2',
      conversationId: 'conv-2',
      resumePrompt: '',
      alsoReopen: [ref('agent-1', 'conv-1')],
    },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-2', conversationId: 'conv-2', resumePrompt: '' },
    alsoReopen: [ref('agent-1', 'conv-1')],
  });
});

// The hand-off names the conversation that asked for the restart, not just the
// orchestrator it belongs to: an id is reusable, so several conversations answer
// to it and continuing the wrong one wakes a session in a scope it never had.
// Only the pane owner can carry the auto-run prompt — alsoReopen entries are
// always idle, but still each resume their own resolved conversation.
test('a restart hand-off carries the conversation and the prompt it auto-runs', () => {
  const outcome = planOrchestratorRestore(
    {
      orchestratorId: 'agent-1',
      conversationId: 'conv-1',
      resumePrompt: 'finish the task',
      alsoReopen: [ref('agent-2', 'conv-2')],
    },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-1', conversationId: 'conv-1', resumePrompt: 'finish the task' },
    alsoReopen: [ref('agent-2', 'conv-2')],
  });
});

test('nothing to reopen leaves boot on the default environment selection', () => {
  assert.deepEqual(planOrchestratorRestore({ orchestratorId: '' }, persisted), {
    primary: null,
    alsoReopen: [],
  });
  assert.deepEqual(planOrchestratorRestore(null, persisted), { primary: null, alsoReopen: [] });
  assert.deepEqual(planOrchestratorRestore(undefined, persisted), {
    primary: null,
    alsoReopen: [],
  });
  assert.deepEqual(planOrchestratorRestore({ orchestratorId: '   ' }, persisted), {
    primary: null,
    alsoReopen: [],
  });
});

// A pane owner deleted since the record was written cannot be reopened, but the
// rest of the set is not thrown away with it — the next most-recently-opened
// survivor takes the pane instead, carrying the conversation the backend
// already resolved for it rather than losing it on promotion.
test('a pane owner that no longer exists is dropped and a survivor is promoted', () => {
  const outcome = planOrchestratorRestore(
    {
      orchestratorId: 'agent-gone',
      alsoReopen: [ref('agent-1', 'conv-1'), ref('agent-2', 'conv-2')],
    },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-2', conversationId: 'conv-2', resumePrompt: '' },
    alsoReopen: [ref('agent-1', 'conv-1')],
  });
});

// When nothing in the whole set survives, boot has nothing to fall back to but
// the default environment selection.
test('a target that no longer exists anywhere restores nothing', () => {
  assert.deepEqual(planOrchestratorRestore({ orchestratorId: 'agent-gone' }, persisted), {
    primary: null,
    alsoReopen: [],
  });
});

// Investigate sessions are not persisted, so they are never reopened, whether
// named as the pane owner or listed in alsoReopen.
test('a transient orchestrator is never restored', () => {
  const transientOnly = [orchestrator('investigate-1', true)];
  assert.deepEqual(planOrchestratorRestore({ orchestratorId: 'investigate-1' }, transientOnly), {
    primary: null,
    alsoReopen: [],
  });
  assert.deepEqual(
    planOrchestratorRestore(
      {
        orchestratorId: 'agent-1',
        conversationId: 'conv-1',
        alsoReopen: [ref('investigate-1', 'conv-x')],
      },
      [...persisted, ...transientOnly],
    ),
    { primary: { id: 'agent-1', conversationId: 'conv-1', resumePrompt: '' }, alsoReopen: [] },
  );
});

// A duplicate or an id that also happens to be the pane owner must not spawn a
// second session for the same orchestrator.
test('alsoReopen drops duplicates and the pane owner itself', () => {
  const outcome = planOrchestratorRestore(
    {
      orchestratorId: 'agent-1',
      conversationId: 'conv-1',
      alsoReopen: [ref('agent-1', 'conv-1'), ref('agent-2', 'conv-2'), ref('agent-2', 'conv-2')],
    },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-1', conversationId: 'conv-1', resumePrompt: '' },
    alsoReopen: [ref('agent-2', 'conv-2')],
  });
});

// A prompt that is only whitespace is no prompt: it must not push the launch
// down the auto-run branch.
test('a whitespace-only resume prompt resumes idle', () => {
  const outcome = planOrchestratorRestore(
    { orchestratorId: 'agent-1', conversationId: 'conv-1', resumePrompt: '  \n ' },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-1', conversationId: 'conv-1', resumePrompt: '' },
    alsoReopen: [],
  });
});

// The backend may resolve nothing safe to resume (no live session was ever
// recorded, or the recorded one is gone) — the plan must carry that through as
// an empty conversationId rather than inventing one, so the caller starts the
// orchestrator fresh instead of resuming a guess.
test('no resolved conversation plans a fresh start, not a guessed one', () => {
  const outcome = planOrchestratorRestore(
    { orchestratorId: 'agent-1', resumePrompt: '' },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-1', conversationId: '', resumePrompt: '' },
    alsoReopen: [],
  });
});

// The target crosses a process boundary, so a payload without the field must
// read as "no refusal" rather than throw and take the restore down with it.
test('a missing notice reads as no refusal', () => {
  assert.equal(readRestoreNotice({ orchestratorId: 'agent-1', resumePrompt: '' }), '');
  assert.equal(readRestoreNotice(null), '');
  assert.equal(
    readRestoreNotice({ orchestratorId: 'agent-1', notice: '  scope changed  ' }),
    'scope changed',
  );
});
