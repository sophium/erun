import assert from 'node:assert/strict';

import { configureStore } from '@reduxjs/toolkit';
import { test } from 'vitest';

import type { UIEnvironmentConfig, UISelection, UITenant } from '@/types';

import { ensureDefaultEnvTabs, openSelection } from './sessionThunks';
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
    launchSettingsChanged: false,
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

// A runtime env with a stopped cloud context makes resolveAutoStartGate await LoadEnvironmentConfig.
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

    // Same shape as focusOrchestratorSession.
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

// A local-agent env's gate resolves synchronously, so the race lands inside StartSession.
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

    // Only terminal.sessionId moves; the sidebar still names "alpha".
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

function hostTenant(): UITenant {
  return {
    name: 'acme',
    environments: [{ name: 'alpha', type: 'host' }],
  };
}

test('a host environment spawns only its Local tab, never the pod-shaped ERun tab', async () => {
  let startSessionCalls = 0;
  let startLocalCalls = 0;
  stubWailsBridge({
    StartLocalSession: () => {
      startLocalCalls += 1;
      return Promise.resolve({ sessionId: 7, slot: 0 });
    },
    StartSession: () => {
      startSessionCalls += 1;
      return Promise.resolve({ sessionId: 8, slot: 0 });
    },
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  store.dispatch(setTenants([hostTenant()]));
  thunkExtra.controller = {
    fitTerminal: () => undefined,
    terminalSize: () => ({ cols: 80, rows: 24 }),
  } as unknown as TerminalController | null;

  await dispatch(
    ensureDefaultEnvTabs({ tenant: 'acme', environment: 'alpha' }, 'acme/alpha', 80, 24),
  );

  assert.equal(startSessionCalls, 0, 'a host env must not spawn the ERun tab');
  assert.equal(startLocalCalls, 1, 'a host env opens its Local tab');
});

test('a pod-backed environment still spawns its ERun tab beside Local', async () => {
  let startSessionCalls = 0;
  let startLocalCalls = 0;
  stubWailsBridge({
    StartLocalSession: () => {
      startLocalCalls += 1;
      return Promise.resolve({ sessionId: 7, slot: 0 });
    },
    StartSession: () => {
      startSessionCalls += 1;
      return Promise.resolve({ sessionId: 8, slot: 0 });
    },
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  store.dispatch(setTenants([localAgentTenant()]));
  thunkExtra.controller = {
    fitTerminal: () => undefined,
    terminalSize: () => ({ cols: 80, rows: 24 }),
  } as unknown as TerminalController | null;

  await dispatch(
    ensureDefaultEnvTabs({ tenant: 'acme', environment: 'alpha' }, 'acme/alpha', 80, 24),
  );

  assert.equal(startSessionCalls, 1, 'a pod-backed env still spawns its ERun tab');
  assert.equal(startLocalCalls, 1, 'and its Local tab');
});
