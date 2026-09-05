// Side-effect import: stateApi injects its endpoint into the shared wailsApi
// instance on import.
import './api/stateApi';

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';

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

// Mirrors platformSignInRecovery.test.ts's RPC-boundary stub: every
// wailsQueryFn-backed endpoint calls through window.go.main.App.*, so
// standing that up by hand drives the real production dispatch path (the
// actual stateApi endpoint, real RTK Query middleware) instead of asserting
// dispatch shape and stopping there.
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

// erun#1953: an 'environments-changed' event that arrived while a
// getInitialState fetch was already in flight got silently dropped —
// RTK Query's condition() check bails a forced refetch out from under a
// pending request for the same query before ever looking at forceRefetch,
// so the caller's own fresh load, not the invalidation's, decided what
// landed. reloadStateAfterEnvironmentChange now waits for the in-flight
// request via getRunningQueryThunk before issuing its own forced refetch.
// This proves the fix behaviourally: an invalidation arriving mid-flight
// must still end up with a second, real fetch and fresh data, not the
// first fetch's stale result.
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

  // Simulate boot()'s own in-flight load.
  const inFlight = dispatch(stateApi.endpoints.getInitialState.initiate(undefined)).unwrap();
  await waitFor(() => pendingCalls.length === 1);

  // The invalidation arrives while that load is still in flight.
  const reloaded = dispatch(reloadStateAfterEnvironmentChange());

  // The reload must wait for the in-flight request rather than starting a
  // second one immediately.
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(
    pendingCalls.length,
    1,
    'reload must wait for the in-flight request instead of racing it',
  );

  resolveCall(pendingCalls, 0, { tenants: [tenant('stale')] });
  await inFlight;

  // Only now should the forced refetch actually run.
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
    ...overrides,
  };
}

// getInitialState is a real backend round trip, so an orchestrator session
// can already have been focused (the headless harness's own reconnect
// handoff, or a concurrent user action) by the time it resolves. boot() used
// to dispatch setSelected(loaded.selected) unconditionally at that point --
// and setSelected's own selection-sync middleware (selectionSyncMiddleware)
// reconciles terminal.sessionId onto whatever environment that selection
// names, clobbering the orchestrator's session with a stale or empty one.
// This is the root cause behind orchestrator-cross-env-diff.spec.ts's flake:
// the same race the openSelection-side fix in sessionThunks.ts (isDefaultLandingOpen)
// guards is reachable through this earlier, separate dispatch too.
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

  // The orchestrator session claims the terminal while getInitialState is
  // still in flight -- the same shape focusOrchestratorSession takes.
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
