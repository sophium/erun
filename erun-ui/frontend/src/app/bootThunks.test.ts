// Side-effect import: registers the stateApi endpoint.
import './api/stateApi';

import assert from 'node:assert/strict';

import { configureStore } from '@reduxjs/toolkit';
import { test } from 'vitest';

import type { UIState, UITenant } from '@/types';

import { stateApi } from './api/stateApi';
import { wailsApi } from './api/wailsApi';
import { boot, reloadStateAfterEnvironmentChange } from './bootThunks';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';
import orchestratorsReducer from './slices/orchestratorsSlice';
import selectionReducer from './slices/selectionSlice';
import tenantsReducer from './slices/tenantsSlice';
import terminalReducer, { setSessionId } from './slices/terminalSlice';
import type { AppDispatch } from './store';

// Stubs the Wails bridge so the real endpoint and RTK Query middleware run.
function stubWailsBridge(app: Record<string, (...args: never[]) => Promise<unknown>>): void {
  (globalThis as unknown as { window: unknown }).window = { go: { main: { App: app } } };
}

function buildTestStore() {
  return configureStore({
    reducer: {
      tenants: tenantsReducer,
      [wailsApi.reducerPath]: wailsApi.reducer,
    },
    middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(wailsApi.middleware),
  });
}

function tenant(name: string): UITenant {
  return { name, environments: [] };
}

interface Deferred {
  resolve: (value: UIState) => void;
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

function resolveCall(calls: Deferred[], index: number, state: UIState): void {
  const call = calls[index];
  assert.ok(call, `expected a pending call at index ${String(index)}`);
  call.resolve(state);
}

test('an invalidation arriving during an in-flight getInitialState still produces a fresh refetch', async () => {
  const pendingCalls: Deferred[] = [];
  stubWailsBridge({
    LoadState: () =>
      new Promise<UIState>((resolve) => {
        pendingCalls.push({ resolve });
      }),
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;

  const inFlight = dispatch(stateApi.endpoints.getInitialState.initiate(undefined)).unwrap();
  await waitFor(() => pendingCalls.length === 1);

  const reloaded = dispatch(reloadStateAfterEnvironmentChange());

  // The reload must wait rather than start a second request.
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(
    pendingCalls.length,
    1,
    'reload must wait for the in-flight request instead of racing it',
  );

  resolveCall(pendingCalls, 0, { tenants: [tenant('stale')] });
  await inFlight;

  await waitFor(() => pendingCalls.length === 2);
  resolveCall(pendingCalls, 1, { tenants: [tenant('fresh')] });
  await reloaded;

  assert.deepEqual(
    store.getState().tenants.tenants.map((t) => t.name),
    ['fresh'],
    'the invalidation must land its own fresh data, not the in-flight request’s stale result',
  );
});

function buildBootTestStore() {
  return configureStore({
    reducer: {
      orchestrators: orchestratorsReducer,
      selection: selectionReducer,
      tenants: tenantsReducer,
      terminal: terminalReducer,
      [wailsApi.reducerPath]: wailsApi.reducer,
    },
    middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(wailsApi.middleware),
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

test('boot() does not restore the persisted selection once an orchestrator session already owns the terminal', async () => {
  const pendingLoadState: { resolve: (value: unknown) => void }[] = [];
  stubWailsBridge({
    LoadState: () =>
      new Promise((resolve) => {
        pendingLoadState.push({ resolve });
      }),
    ConsumeInterruptedActivityNotice: () => Promise.resolve(null),
    ListOrchestrators: () => Promise.resolve([fakeOrchestrator({ id: 'orch', sessionId: 999 })]),
    ListDeploys: () => Promise.resolve([]),
    ResolveOrchestratorToReopen: () => Promise.resolve(null),
  });

  const store = buildBootTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;

  const booted = dispatch(boot());
  await waitFor(() => pendingLoadState.length === 1);

  // An orchestrator claims the terminal while getInitialState is in flight.
  store.dispatch(setSessionId(999));

  pendingLoadState[0]?.resolve({
    tenants: [],
    selected: { tenant: 'acme', environment: 'alpha' },
  });
  await booted;

  assert.equal(
    store.getState().selection.selected,
    null,
    'boot() must not restore the persisted selection over an orchestrator session that already claimed the terminal',
  );
  assert.equal(store.getState().terminal.sessionId, 999, 'the orchestrator must keep the terminal');
});
