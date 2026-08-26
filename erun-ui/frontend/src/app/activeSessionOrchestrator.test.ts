import assert from 'node:assert/strict';
import { test } from 'node:test';

import { selectActiveSessionOrchestrator, selectIsOrchestratorSession } from './selectors';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';
import type { RootState } from './store';

function orchestrator(id: string, sessionId: number): OrchestratorInfo {
  return {
    id,
    name: id,
    environments: [{ tenant: 'erun', environment: 'ux', directory: '/tmp/erun-ux' }],
    tenants: ['erun'],
    directories: ['/tmp/erun-ux'],
    sessionId,
    status: sessionId > 0 ? 'running' : 'stopped',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    nudgeCapped: false,
    restartRequired: false,
  };
}

// Only the two slices the selector reads; the rest of RootState is irrelevant to
// it and constructing it would make the test about the store's shape instead.
function stateWith(activeSessionId: number, items: OrchestratorInfo[]): RootState {
  return {
    terminal: { sessionId: activeSessionId },
    orchestrators: { items },
  } as unknown as RootState;
}

// The defect this selector exists to fix: the titlebar keyed its env-scoped
// controls off state.selection.selected -- the SIDEBAR's environment selection,
// which is independent of which terminal tab is active. So with an orchestrator
// tab focused, "Open in VS Code" acted on an environment the orchestrator may
// not even be linked to. The active SESSION is the thing that decides whether
// env-scoped chrome applies, and that is what this reads.
test('the active session being an orchestrator is decided by the session, not the sidebar', () => {
  const state = stateWith(42, [orchestrator('erun-issues', 42), orchestrator('other', 7)]);

  const active = selectActiveSessionOrchestrator(state);
  assert.equal(active?.id, 'erun-issues');
  assert.equal(selectIsOrchestratorSession(state), true);
});

test('an environment tab is not orchestrator mode', () => {
  // A session id that belongs to no orchestrator: an ordinary env tab.
  const state = stateWith(99, [orchestrator('erun-issues', 42)]);

  assert.equal(selectActiveSessionOrchestrator(state), null);
  assert.equal(selectIsOrchestratorSession(state), false);
});

// A stopped orchestrator carries sessionId 0. Without the guard, an inactive
// terminal (also 0) would match it and the titlebar would drop its env controls
// while no orchestrator was running at all.
test('no active session is not orchestrator mode, even with a stopped orchestrator present', () => {
  const state = stateWith(0, [orchestrator('stopped-one', 0)]);

  assert.equal(selectActiveSessionOrchestrator(state), null);
  assert.equal(selectIsOrchestratorSession(state), false);
});

// The selector returns the orchestrator, not a boolean, because a cross-env
// surface needs its environment list -- the diff panel has to fetch one diff per
// linked environment.
test('the active orchestrator carries the environments a cross-env surface needs', () => {
  const active = selectActiveSessionOrchestrator(stateWith(42, [orchestrator('erun-issues', 42)]));

  assert.ok(active, 'expected the active session to resolve an orchestrator');
  assert.equal(active.environments.length, 1);
  const [env] = active.environments;
  // assert.ok narrows: the tsconfig has noUncheckedIndexedAccess, so a
  // destructured element is possibly-undefined, while eslint's type-aware rule
  // reads an optional chain here as redundant. Asserting satisfies both.
  assert.ok(env, 'expected one linked environment');
  assert.equal(env.tenant, 'erun');
  assert.equal(env.environment, 'ux');
});
