import assert from 'node:assert/strict';

import { test } from 'vitest';

import { planOrchestratorBusySeed } from './orchestratorBusySeed';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';

function orchestrator(sessionId: number, busy: boolean): OrchestratorInfo {
  return {
    id: 'agent-1',
    name: 'agent-1',
    environments: [],
    tenants: [],
    directories: [],
    sessionId,
    status: sessionId > 0 ? 'running' : 'stopped',
    busy,
    transient: false,
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

// A fetch alone must seed busy; the ai-activity event may have fired before the
// listener attached.
test('a running orchestrator seeds its session busy from the snapshot alone', () => {
  const seed = planOrchestratorBusySeed([orchestrator(7, true)]);
  assert.deepEqual(seed, [{ sessionId: 7, busy: true }]);
});

test('a running orchestrator that is not busy seeds false, clearing a stale entry', () => {
  const seed = planOrchestratorBusySeed([orchestrator(7, false)]);
  assert.deepEqual(seed, [{ sessionId: 7, busy: false }]);
});

test('a stopped orchestrator contributes no seed', () => {
  assert.deepEqual(planOrchestratorBusySeed([orchestrator(0, false)]), []);
});

test('mixed running and stopped orchestrators seed only the running ones', () => {
  const seed = planOrchestratorBusySeed([
    orchestrator(1, true),
    orchestrator(0, false),
    orchestrator(2, false),
  ]);
  assert.deepEqual(seed, [
    { sessionId: 1, busy: true },
    { sessionId: 2, busy: false },
  ]);
});
