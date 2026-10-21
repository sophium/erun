import assert from 'node:assert/strict';

import { test } from 'vitest';

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
    busy: false,
    transient,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    autoNudgeCount: 0,
    whipCount: 0,
    nudgeCapped: false,
    restartRequired: false,
    roleChanged: false,
    launchSettingsChanged: false,
  };
}

function ref(orchestratorId: string) {
  return { orchestratorId };
}

const persisted = [orchestrator('agent-1'), orchestrator('agent-2')];

// The defect: a plain quit-and-relaunch had no target at all, so boot fell
// through to the default environment selection. The durable record now answers.
test('a plain launch reopens the orchestrator that was open', () => {
  assert.deepEqual(planOrchestratorRestore({ orchestratorId: 'agent-1' }, persisted), {
    primary: 'agent-1',
    alsoReopen: [],
  });
});

// The durable record was once a scalar, so only one orchestrator ever came
// back. Every id in alsoReopen must be restored too.
test('every orchestrator that was open is restored, not just the pane owner', () => {
  assert.deepEqual(
    planOrchestratorRestore({ orchestratorId: 'agent-2', alsoReopen: [ref('agent-1')] }, persisted),
    { primary: 'agent-2', alsoReopen: ['agent-1'] },
  );
});

test('an empty or missing target restores nothing', () => {
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
  assert.deepEqual(
    planOrchestratorRestore(
      { orchestratorId: 'agent-gone', alsoReopen: [ref('agent-1'), ref('agent-2')] },
      persisted,
    ),
    { primary: 'agent-2', alsoReopen: ['agent-1'] },
  );
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
    planOrchestratorRestore({ orchestratorId: 'agent-1', alsoReopen: [ref('investigate-1')] }, [
      ...persisted,
      ...transientOnly,
    ]),
    { primary: 'agent-1', alsoReopen: [] },
  );
});

// A duplicate or an id that also happens to be the pane owner must not spawn a
// second session for the same orchestrator.
test('alsoReopen drops duplicates and the pane owner itself', () => {
  assert.deepEqual(
    planOrchestratorRestore(
      { orchestratorId: 'agent-1', alsoReopen: [ref('agent-1'), ref('agent-2'), ref('agent-2')] },
      persisted,
    ),
    { primary: 'agent-1', alsoReopen: ['agent-2'] },
  );
});
