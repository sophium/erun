// Side-effect import: idleApi registers its endpoint on the shared wailsApi.
import './api/idleApi';

import assert from 'node:assert/strict';

import { configureStore } from '@reduxjs/toolkit';
import { test } from 'vitest';

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

// RTK Query bails a forced refetch while the same query is in flight, so a second
// trigger must still get a fresh fetch.
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
    const polled = dispatch(refreshIdleStatus());
    await waitFor(() => pendingCalls.length === 1);

    const triggered = dispatch(refreshIdleStatus());

    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(
      pendingCalls.length,
      1,
      'the second refresh must wait for the in-flight request instead of racing it',
    );

    resolveCall(pendingCalls, 0, idleStatus('stopped'));
    await polled;

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
