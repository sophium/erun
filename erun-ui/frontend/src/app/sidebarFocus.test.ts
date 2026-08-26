import assert from 'node:assert/strict';
import { test } from 'node:test';

import { selectSidebarFocus } from './selectors';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';
import type { RootState } from './store';

function orchestrator(id: string, sessionId: number): OrchestratorInfo {
  return {
    id,
    name: id,
    environments: [],
    tenants: [],
    directories: [],
    sessionId,
    status: sessionId > 0 ? 'running' : 'stopped',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    nudgeCapped: false,
  };
}

// Only the four slices selectSidebarFocus reads; the rest of RootState is
// irrelevant here and constructing it would make the test about the store's
// shape instead of the derivation.
function stateWith(fields: {
  dashboardTenant?: string;
  sessionId?: number;
  orchestrators?: OrchestratorInfo[];
  selected?: { tenant: string; environment: string } | null;
}): RootState {
  return {
    tenantDashboard: { tenant: fields.dashboardTenant ?? '' },
    terminal: { sessionId: fields.sessionId ?? 0 },
    orchestrators: { items: fields.orchestrators ?? [] },
    selection: { selected: fields.selected ?? null },
  } as unknown as RootState;
}

// #1204: the tenant dashboard, an orchestrator's session, and an
// environment's session used to be three independently-computed "active"
// booleans across three components. selectSidebarFocus is the single value
// they must all derive from instead, so at most one of them is ever true.

test('an open tenant dashboard wins even over a stale active session', () => {
  // A dashboard opened while an orchestrator's session was still the last
  // thing focused: terminal.sessionId still matches that orchestrator, but
  // the dashboard must still be what's reported as focused.
  const state = stateWith({
    dashboardTenant: 'acme',
    sessionId: 42,
    orchestrators: [orchestrator('erun-issues', 42)],
  });

  assert.deepEqual(selectSidebarFocus(state), { kind: 'dashboard', tenant: 'acme' });
});

test('an orchestrator session wins over a stale environment selection', () => {
  const state = stateWith({
    sessionId: 42,
    orchestrators: [orchestrator('erun-issues', 42)],
    selected: { tenant: 'acme', environment: 'prod' },
  });

  assert.deepEqual(selectSidebarFocus(state), { kind: 'orchestrator', sessionId: 42 });
});

test('an environment selection is reported once no dashboard or orchestrator applies', () => {
  const state = stateWith({ selected: { tenant: 'acme', environment: 'prod' } });

  assert.deepEqual(selectSidebarFocus(state), {
    kind: 'environment',
    tenant: 'acme',
    environment: 'prod',
  });
});

test('nothing focused reports none', () => {
  assert.deepEqual(selectSidebarFocus(stateWith({})), { kind: 'none' });
});

test('a terminal session that matches no orchestrator is not orchestrator focus', () => {
  const state = stateWith({
    sessionId: 99,
    orchestrators: [orchestrator('erun-issues', 42)],
    selected: { tenant: 'acme', environment: 'prod' },
  });

  assert.deepEqual(selectSidebarFocus(state), {
    kind: 'environment',
    tenant: 'acme',
    environment: 'prod',
  });
});
