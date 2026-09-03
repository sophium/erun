import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';

import type { UIEnvironmentConfig, UISelection, UITenant } from '@/types';

import { openSelection } from './sessionThunks';
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
    const opened = dispatch(openSelection(selection));
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
