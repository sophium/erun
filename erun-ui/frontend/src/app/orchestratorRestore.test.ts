import assert from 'node:assert/strict';
import { test } from 'node:test';

import { planOrchestratorRestore } from './orchestratorRestore';
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
    transient,
  };
}

const persisted = [orchestrator('agent-1')];

// The defect: a plain quit-and-relaunch had no target at all, so boot fell
// through to the default environment selection. The durable record now answers,
// and it answers without a prompt — the launch resumes idle.
test('a plain launch reopens the orchestrator that was open, running nothing', () => {
  const plan = planOrchestratorRestore({ orchestratorId: 'agent-1', resumePrompt: '' }, persisted);
  assert.deepEqual(plan, { id: 'agent-1', resumePrompt: '' });
});

test('a restart hand-off carries the prompt the resumed session auto-runs', () => {
  const plan = planOrchestratorRestore(
    { orchestratorId: 'agent-1', resumePrompt: 'finish the task' },
    persisted,
  );
  assert.deepEqual(plan, { id: 'agent-1', resumePrompt: 'finish the task' });
});

test('nothing to reopen leaves boot on the default environment selection', () => {
  assert.equal(planOrchestratorRestore({ orchestratorId: '' }, persisted), null);
  assert.equal(planOrchestratorRestore(null, persisted), null);
  assert.equal(planOrchestratorRestore(undefined, persisted), null);
  assert.equal(planOrchestratorRestore({ orchestratorId: '   ' }, persisted), null);
});

// An orchestrator deleted since the record was written has no definition to
// resume, so the launch must not try to start it.
test('a target that no longer exists restores nothing', () => {
  assert.equal(planOrchestratorRestore({ orchestratorId: 'agent-gone' }, persisted), null);
});

// Investigate sessions are not persisted, so they are never reopened.
test('a transient orchestrator is never restored', () => {
  assert.equal(
    planOrchestratorRestore({ orchestratorId: 'investigate-1' }, [
      orchestrator('investigate-1', true),
    ]),
    null,
  );
});

// A prompt that is only whitespace is no prompt: it must not push the launch
// down the auto-run branch.
test('a whitespace-only resume prompt resumes idle', () => {
  const plan = planOrchestratorRestore(
    { orchestratorId: 'agent-1', resumePrompt: '  \n ' },
    persisted,
  );
  assert.deepEqual(plan, { id: 'agent-1', resumePrompt: '' });
});
