// Side-effect import: idleApi injects its endpoint into the shared wailsApi
// instance on import.
import './api/idleApi';

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';

import type { UIIdleStatus, UISelection } from '@/types';

import { idleApi } from './api/idleApi';
import { wailsApi } from './api/wailsApi';
import { refreshIdleStatus } from './idleThunks';
import idleReducer from './slices/idleSlice';
import requestCountersReducer from './slices/requestCountersSlice';
import selectionReducer, { setSelected } from './slices/selectionSlice';
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
      idle: idleReducer,
      requestCounters: requestCountersReducer,
      selection: selectionReducer,
      [wailsApi.reducerPath]: wailsApi.reducer,
    },
    middleware: (getDefaultMiddleware) =>
      getDefaultMiddleware({ thunk: { extraArgument: thunkExtra } }).concat(wailsApi.middleware),
  });
}

interface Deferred {
  resolve: (value: UIIdleStatus) => void;
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

function resolveCall(calls: Deferred[], index: number, status: UIIdleStatus): void {
  const call = calls[index];
  assert.ok(call, `expected a pending call at index ${String(index)}`);
  call.resolve(status);
}

function idleStatus(cloudContextStatus: string): UIIdleStatus {
  return { cloudContextStatus } as UIIdleStatus;
}

// refreshIdleStatus has more
// than one independent trigger for the same selection -- its own
// self-rescheduling poll, selectionSyncMiddleware on an env switch, a
// cloud-context start/stop, and cancelPendingIdleStop. RTK Query's
// condition() check bails a forced refetch out from under a pending request
// for the same query before ever looking at forceRefetch, so a second
// trigger landing while the poll's own request is still in flight used to
// silently inherit that request's pre-event status. This proves the fix
// behaviourally: a refresh triggered mid-flight still ends up with a second,
// real fetch and fresh data, not the first fetch's stale result.
test('a refresh triggered during an in-flight getIdleStatus still produces a fresh fetch', async () => {
  const pendingCalls: Deferred[] = [];
  stubWailsBridge({
    LoadIdleStatus: () =>
      new Promise<UIIdleStatus>((resolve) => {
        pendingCalls.push({ resolve });
      }),
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  const selection: UISelection = { tenant: 'acme', environment: 'dev' };
  store.dispatch(setSelected(selection));
  thunkExtra.controller = {
    scheduleIdleStatusPoll: () => undefined,
  } as unknown as TerminalController | null;

  try {
    // Simulate the self-rescheduling poll's own in-flight request.
    const polled = dispatch(refreshIdleStatus());
    await waitFor(() => pendingCalls.length === 1);

    // A second trigger (e.g. cancelPendingIdleStop) fires while that poll is
    // still in flight.
    const triggered = dispatch(refreshIdleStatus());

    // The second refresh must wait for the in-flight request rather than
    // starting a second one immediately.
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(
      pendingCalls.length,
      1,
      'the second refresh must wait for the in-flight request instead of racing it',
    );

    resolveCall(pendingCalls, 0, idleStatus('stopped'));
    await polled;

    // Only now should the second refresh's forced refetch actually run.
    await waitFor(() => pendingCalls.length === 2);
    resolveCall(pendingCalls, 1, idleStatus('running'));
    await triggered;

    assert.equal(
      store.getState().idle.idleStatus?.cloudContextStatus,
      'running',
      'the later trigger must land its own fresh data, not the in-flight request’s stale result',
    );
  } finally {
    thunkExtra.controller = null;
    dispatch(idleApi.util.resetApiState());
  }
});
