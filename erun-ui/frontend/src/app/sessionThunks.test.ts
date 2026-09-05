import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';

import type { UIEnvironmentConfig, UISelection, UITenant } from '@/types';

import { openSelection } from './sessionThunks';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';
import orchestratorsReducer, { setOrchestrators } from './slices/orchestratorsSlice';
import selectionReducer, { setSelected } from './slices/selectionSlice';
import sessionsReducer from './slices/sessionsSlice';
import tenantDashboardReducer from './slices/tenantDashboardSlice';
import tenantsReducer, { setTenants } from './slices/tenantsSlice';
import terminalReducer, { setSessionId } from './slices/terminalSlice';
import type { AppDispatch } from './store';
import type { TerminalController } from './TerminalController';
import { thunkExtra } from './thunkExtra';

// Mirrors bootThunks.test.ts's RPC-boundary stub.
function stubWailsBridge(app: Record<string, (...args: never[]) => Promise<unknown>>): void {
  (globalThis as unknown as { window: unknown }).window = { go: { main: { App: app } } };
}

function buildTestStore() {
  return configureStore({
    reducer: {
      orchestrators: orchestratorsReducer,
      selection: selectionReducer,
      sessions: sessionsReducer,
      tenantDashboard: tenantDashboardReducer,
      tenants: tenantsReducer,
      terminal: terminalReducer,
    },
    middleware: (getDefaultMiddleware) =>
      getDefaultMiddleware({ thunk: { extraArgument: thunkExtra } }),
  });
}

function fakeOrchestrator(overrides: Partial<OrchestratorInfo> = {}): OrchestratorInfo {
  return {
    id: 'orch',
    name: 'orch',
    environments: [],
    tenants: [],
    directories: [],
    sessionId: 0,
    status: 'running',
    busy: false,
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
    ...overrides,
  };
}

async function waitFor(predicate: () => boolean, timeoutMs = 1000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!predicate()) {
    if (Date.now() > deadline) {
      throw new Error('timed out waiting for condition');
    }
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}

// A "runtime" env with autoStart unset and a stopped cloud context forces
// resolveAutoStartGate to await LoadEnvironmentConfig -- the controllable gap
// this test races through.
function runtimeTenant(): UITenant {
  return {
    name: 'acme',
    environments: [{ name: 'alpha', type: 'runtime' }],
  };
}

function stoppedEnvironmentConfig(): UIEnvironmentConfig {
  return {
    cloudContext: { status: 'stopped' },
  } as unknown as UIEnvironmentConfig;
}

// boot()'s own automatic open of the tenant's default-landing environment
// races an orchestrator session's own focus: the desktop launches,
// boot() decides to open the default env because nothing else had claimed the
// terminal *yet*, then while this openSelection call's own auto-start gate is
// still resolving, an orchestrator session gets focused (or restored) and
// takes the terminal pane. openSelection must not resume from that await and
// drag focus back to the stale selection out from under the orchestrator --
// which is exactly what happened: prepareOpenSelection's unconditional
// setSelected reasserted the default env, and the selection-sync middleware
// reconciled state.terminal.sessionId back to it, pruning every other linked
// environment's diff state out of the orchestrator's review panel.
test('a stale openSelection call does not reclaim the terminal after an orchestrator session was focused', async () => {
  const pendingConfigCalls: { resolve: (value: UIEnvironmentConfig) => void }[] = [];
  stubWailsBridge({
    LoadEnvironmentConfig: () =>
      new Promise<UIEnvironmentConfig>((resolve) => {
        pendingConfigCalls.push({ resolve });
      }),
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  store.dispatch(setTenants([runtimeTenant()]));
  thunkExtra.controller = {
    fitTerminal: () => undefined,
    terminalSize: () => ({ cols: 80, rows: 24 }),
  } as unknown as TerminalController | null;

  try {
    const selection: UISelection = { tenant: 'acme', environment: 'alpha' };
    const opened = dispatch(openSelection(selection, { isDefaultLandingOpen: true }));
    await waitFor(() => pendingConfigCalls.length === 1);

    // The orchestrator session claims the terminal while the gate is still
    // resolving -- the same shape focusOrchestratorSession takes: the
    // terminal's session moves to the orchestrator's own session and the
    // sidebar's own selection clears.
    store.dispatch(setSessionId(999));
    store.dispatch(setSelected(null));

    pendingConfigCalls[0]?.resolve(stoppedEnvironmentConfig());
    await opened;

    assert.equal(
      store.getState().selection.selected,
      null,
      'the stale open must not reassert the sidebar selection over the orchestrator focus',
    );
    assert.equal(
      store.getState().terminal.sessionId,
      999,
      'the stale open must not drag the terminal session back to the environment it was opening',
    );
  } finally {
    thunkExtra.controller = null;
  }
});

// A "local-agent" env's resolveAutoStartGate resolves synchronously
// ('proceed', no LoadEnvironmentConfig round trip), so this races through the
// gap the test above cannot reach: StartSession itself, awaited inside
// finishOpenSession. selection.selected never changes here -- an orchestrator
// session is tracked purely via terminal.sessionId (selectActiveSessionOrchestrator),
// so the sidebar's own selection legitimately keeps reading as the env this
// call started opening for as long as it is still selected there. Before this
// fix, finishOpenSession's isCurrentSelection() only compared
// selection.selected, so it still read "current" here and went on to
// reassert terminal.sessionId onto the newly-finished StartSession's result,
// and (had a review panel been open) collapsed loadReviewDiff's target set
// down to this one environment -- pruning every other one an orchestrator
// had linked, which is exactly the shape orchestrator-cross-env-diff.spec.ts
// flaked on under load.
function localAgentTenant(): UITenant {
  return {
    name: 'acme',
    environments: [{ name: 'alpha', type: 'local-agent' }],
  };
}

test('a stale openSelection call does not reclaim the terminal after an orchestrator session was focused mid-StartSession', async () => {
  const pendingStartSessionCalls: {
    resolve: (value: { sessionId: number; slot: number }) => void;
  }[] = [];
  stubWailsBridge({
    StartLocalSession: () => Promise.resolve({ sessionId: 1, slot: 0 }),
    StartSession: () =>
      new Promise((resolve) => {
        pendingStartSessionCalls.push({ resolve });
      }),
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  store.dispatch(setTenants([localAgentTenant()]));
  store.dispatch(
    setOrchestrators([fakeOrchestrator({ id: 'orch', sessionId: 999, status: 'running' })]),
  );
  thunkExtra.controller = {
    fitTerminal: () => undefined,
    terminalSize: () => ({ cols: 80, rows: 24 }),
  } as unknown as TerminalController | null;

  try {
    const selection: UISelection = { tenant: 'acme', environment: 'alpha' };
    const opened = dispatch(openSelection(selection, { isDefaultLandingOpen: true }));
    await waitFor(() => pendingStartSessionCalls.length === 1);

    // The orchestrator session claims the terminal while StartSession is
    // still in flight. Unlike the test above, the sidebar's own selection is
    // left untouched -- it still legitimately names "alpha", the same as
    // this stale call's own target -- so only terminal.sessionId moves.
    store.dispatch(setSessionId(999));

    pendingStartSessionCalls[0]?.resolve({ sessionId: 42, slot: 0 });
    await opened;

    assert.equal(
      store.getState().terminal.sessionId,
      999,
      'the stale open must not drag the terminal session back to the environment it was opening',
    );
  } finally {
    thunkExtra.controller = null;
  }
});

// The two tests above race an orchestrator session claiming focus *during*
// openSelection's own execution. This test covers the gap they cannot reach:
// an orchestrator already owning the terminal *before* openSelection is ever
// called -- boot()'s decision to call it is made from state.selection.selected,
// which an orchestrator never touches, so a slow enough preceding await
// (getInitialState, loadOrchestrators) lets boot() invoke this well after an
// orchestrator has already claimed the terminal, with nothing having changed
// "during" the call at all. Before this fix, nothing at the top of
// openSelection asked whether the terminal was already spoken for, so it went
// on to dispatch prepareOpenSelection's unconditional setSelected, which the
// selection-sync middleware reconciled straight back onto this environment.
test('openSelection is a no-op when an orchestrator session already owns the terminal', async () => {
  let startLocalSessionCalls = 0;
  stubWailsBridge({
    StartLocalSession: () => {
      startLocalSessionCalls += 1;
      return Promise.resolve({ sessionId: 1, slot: 0 });
    },
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  store.dispatch(setTenants([localAgentTenant()]));
  store.dispatch(
    setOrchestrators([fakeOrchestrator({ id: 'orch', sessionId: 999, status: 'running' })]),
  );
  store.dispatch(setSessionId(999));
  thunkExtra.controller = {
    fitTerminal: () => undefined,
    terminalSize: () => ({ cols: 80, rows: 24 }),
  } as unknown as TerminalController | null;

  try {
    const selection: UISelection = { tenant: 'acme', environment: 'alpha' };
    await dispatch(openSelection(selection, { isDefaultLandingOpen: true }));

    assert.equal(
      startLocalSessionCalls,
      0,
      'an orchestrator-owned terminal must stop openSelection before it starts any session',
    );
    assert.equal(
      store.getState().terminal.sessionId,
      999,
      'the orchestrator must keep the terminal',
    );
    assert.equal(
      store.getState().selection.selected,
      null,
      'openSelection must not reassert the sidebar selection over an active orchestrator',
    );
  } finally {
    thunkExtra.controller = null;
  }
});
