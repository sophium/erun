import assert from 'node:assert/strict';
import { test } from 'node:test';

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
  };
}

// The bug: the sidebar spinner was lit only by the ai-activity event,
// so a fetch that lands after the transition — a fresh mount, a window
// reopen, a listener that attached a beat late — never saw it and the row
// read idle for the rest of the turn. The fix is that the snapshot itself now
// carries the answer, so a fetch alone reproduces the correct seed with no
// event required.
test('a running orchestrator seeds its session busy from the snapshot alone', () => {
  const seed = planOrchestratorBusySeed([orchestrator(7, true)]);
  assert.deepEqual(seed, [{ sessionId: 7, busy: true }]);
});

test('a running orchestrator that is not busy seeds false, clearing a stale entry', () => {
  const seed = planOrchestratorBusySeed([orchestrator(7, false)]);
  assert.deepEqual(seed, [{ sessionId: 7, busy: false }]);
});

// A stopped orchestrator has no session id to key the aiBusyBySession map by,
// so it must not contribute a seed entry at all.
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
