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

test('a plain launch reopens the orchestrator that was open', () => {
  assert.deepEqual(planOrchestratorRestore({ orchestratorId: 'agent-1' }, persisted), {
    primary: 'agent-1',
    alsoReopen: [],
  });
});

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

test('a pane owner that no longer exists is dropped and a survivor is promoted', () => {
  assert.deepEqual(
    planOrchestratorRestore(
      { orchestratorId: 'agent-gone', alsoReopen: [ref('agent-1'), ref('agent-2')] },
      persisted,
    ),
    { primary: 'agent-2', alsoReopen: ['agent-1'] },
  );
});

test('a target that no longer exists anywhere restores nothing', () => {
  assert.deepEqual(planOrchestratorRestore({ orchestratorId: 'agent-gone' }, persisted), {
    primary: null,
    alsoReopen: [],
  });
});

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

test('alsoReopen drops duplicates and the pane owner itself', () => {
  assert.deepEqual(
    planOrchestratorRestore(
      { orchestratorId: 'agent-1', alsoReopen: [ref('agent-1'), ref('agent-2'), ref('agent-2')] },
      persisted,
    ),
    { primary: 'agent-1', alsoReopen: ['agent-2'] },
  );
});
