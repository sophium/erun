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
  };
}

const persisted = [orchestrator('agent-1'), orchestrator('agent-2')];

// The defect: a plain quit-and-relaunch had no target at all, so boot fell
// through to the default environment selection. The durable record now answers,
// and it answers without a prompt — the launch resumes idle.
test('a plain launch reopens the orchestrator that was open, running nothing', () => {
  const outcome = planOrchestratorRestore(
    { orchestratorId: 'agent-1', resumePrompt: '' },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-1', conversationId: '', resumePrompt: '' },
    alsoReopen: [],
  });
});

// The bug this issue fixes: the durable record was a scalar, so only one
// orchestrator ever came back. Every id in alsoReopen must be restored too.
test('every orchestrator that was open is restored, not just the pane owner', () => {
  const outcome = planOrchestratorRestore(
    { orchestratorId: 'agent-2', resumePrompt: '', alsoReopen: ['agent-1'] },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-2', conversationId: '', resumePrompt: '' },
    alsoReopen: ['agent-1'],
  });
});

// The hand-off names the conversation that asked for the restart, not just the
// orchestrator it belongs to: an id is reusable, so several conversations answer
// to it and continuing the wrong one wakes a session in a scope it never had.
// Only the pane owner can carry it — alsoReopen ids are always idle.
test('a restart hand-off carries the conversation and the prompt it auto-runs', () => {
  const outcome = planOrchestratorRestore(
    {
      orchestratorId: 'agent-1',
      conversationId: 'conv-1',
      resumePrompt: 'finish the task',
      alsoReopen: ['agent-2'],
    },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-1', conversationId: 'conv-1', resumePrompt: 'finish the task' },
    alsoReopen: ['agent-2'],
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
// survivor takes the pane instead.
test('a pane owner that no longer exists is dropped and a survivor is promoted', () => {
  const outcome = planOrchestratorRestore(
    { orchestratorId: 'agent-gone', alsoReopen: ['agent-1', 'agent-2'] },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-2', conversationId: '', resumePrompt: '' },
    alsoReopen: ['agent-1'],
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
    planOrchestratorRestore({ orchestratorId: 'agent-1', alsoReopen: ['investigate-1'] }, [
      ...persisted,
      ...transientOnly,
    ]),
    { primary: { id: 'agent-1', conversationId: '', resumePrompt: '' }, alsoReopen: [] },
  );
});

// A duplicate or an id that also happens to be the pane owner must not spawn a
// second session for the same orchestrator.
test('alsoReopen drops duplicates and the pane owner itself', () => {
  const outcome = planOrchestratorRestore(
    { orchestratorId: 'agent-1', alsoReopen: ['agent-1', 'agent-2', 'agent-2'] },
    persisted,
  );
  assert.deepEqual(outcome, {
    primary: { id: 'agent-1', conversationId: '', resumePrompt: '' },
    alsoReopen: ['agent-2'],
  });
});

// A prompt that is only whitespace is no prompt: it must not push the launch
// down the auto-run branch.
test('a whitespace-only resume prompt resumes idle', () => {
  const outcome = planOrchestratorRestore(
    { orchestratorId: 'agent-1', resumePrompt: '  \n ' },
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
