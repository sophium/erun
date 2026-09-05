import assert from 'node:assert/strict';
import { test } from 'node:test';

import { planOrchestratorShellSeed } from './orchestratorShellActivitySeed';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';

function orchestrator(
  sessionId: number,
  shellRunning: boolean,
  shellCommand = '',
  shellStartedAtUnix = 0,
): OrchestratorInfo {
  return {
    id: 'agent-1',
    name: 'agent-1',
    environments: [],
    tenants: [],
    directories: [],
    sessionId,
    status: sessionId > 0 ? 'running' : 'stopped',
    busy: false,
    transient: false,
    shellRunning,
    shellCommand,
    shellStartedAtUnix,
    nudgeCount: 0,
    autoNudgeCount: 0,
    whipCount: 0,
    nudgeCapped: false,
    restartRequired: false,
    roleChanged: false,
  };
}

// The point of the snapshot half of the fix (the same treatment the busy
// signal already gets): a fetch that lands after the shell started (boot, a
// reload) must render the running shell without ever having witnessed the
// orchestrator-shell-activity event that announced it.
test('a running orchestrator seeds its shell activity from the snapshot alone', () => {
  const seed = planOrchestratorShellSeed([orchestrator(7, true, 'sleep 300', 1_700_000_000)]);
  assert.deepEqual(seed, [
    { sessionId: 7, running: true, command: 'sleep 300', startedAtUnix: 1_700_000_000 },
  ]);
});

test('an orchestrator with no running shell seeds not-running, clearing a stale entry', () => {
  const seed = planOrchestratorShellSeed([orchestrator(7, false)]);
  assert.deepEqual(seed, [{ sessionId: 7, running: false, command: '', startedAtUnix: 0 }]);
});

// A stopped orchestrator has no session id to key the shell-activity map by,
// so it must not contribute a seed entry at all.
test('a stopped orchestrator contributes no seed', () => {
  assert.deepEqual(planOrchestratorShellSeed([orchestrator(0, false)]), []);
});

test('mixed running and stopped orchestrators seed only the running ones', () => {
  const seed = planOrchestratorShellSeed([
    orchestrator(1, true, 'sleep 300', 1_700_000_000),
    orchestrator(0, false),
    orchestrator(2, false),
  ]);
  assert.deepEqual(seed, [
    { sessionId: 1, running: true, command: 'sleep 300', startedAtUnix: 1_700_000_000 },
    { sessionId: 2, running: false, command: '', startedAtUnix: 0 },
  ]);
});
