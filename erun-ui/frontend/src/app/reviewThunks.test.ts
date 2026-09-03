// Side-effect import: reviewApi injects its endpoint into the shared
// wailsApi instance on import.
import './api/reviewApi';

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';

import type { DiffResult, UISelection } from '@/types';

import { wailsApi } from './api/wailsApi';
import { loadReviewDiff } from './reviewThunks';
import contributeReducer from './slices/contributeSlice';
import diffReviewStatusReducer from './slices/diffReviewStatusSlice';
import layoutReducer from './slices/layoutSlice';
import requestCountersReducer from './slices/requestCountersSlice';
import reviewReducer from './slices/reviewSlice';
import selectionReducer, { setSelected } from './slices/selectionSlice';
import terminalReducer from './slices/terminalSlice';
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
      contribute: contributeReducer,
      diffReviewStatus: diffReviewStatusReducer,
      layout: layoutReducer,
      requestCounters: requestCountersReducer,
      review: reviewReducer,
      selection: selectionReducer,
      terminal: terminalReducer,
      [wailsApi.reducerPath]: wailsApi.reducer,
    },
    middleware: (getDefaultMiddleware) =>
      getDefaultMiddleware({ thunk: { extraArgument: thunkExtra } }).concat(wailsApi.middleware),
  });
}

interface Deferred {
  resolve: (value: DiffResult) => void;
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

function resolveCall(calls: Deferred[], index: number, diff: DiffResult): void {
  const call = calls[index];
  assert.ok(call, `expected a pending call at index ${String(index)}`);
  call.resolve(diff);
}

function diffResult(rawDiff: string): DiffResult {
  return {
    files: [],
    rawDiff,
    summary: { fileCount: 0, additions: 0, deletions: 0 },
  };
}

// erun#1981 (audit follow-up to #1953/#1976): loadReviewDiff's own periodic
// silent refresh (scheduleReviewDiffRefresh) and a manual "Refresh diff"
// click both dispatch a forced getDiff refetch for the same env. The timer's
// own re-arm only skips a tick while ITS OWN previous request is loading, not
// a manual click landing mid-tick. RTK Query's condition() check bails a
// forced refetch out from under a pending request for the same query before
// ever looking at forceRefetch, so a click racing the timer used to silently
// inherit the timer's in-flight, pre-click diff. This proves the fix
// behaviourally: a load triggered mid-flight still ends up with a second,
// real fetch and fresh data, not the first fetch's stale result.
test('a diff load triggered during an in-flight getDiff still produces a fresh fetch', async () => {
  const pendingCalls: Deferred[] = [];
  stubWailsBridge({
    LoadDiff: () =>
      new Promise<DiffResult>((resolve) => {
        pendingCalls.push({ resolve });
      }),
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  const selection: UISelection = { tenant: 'acme', environment: 'dev' };
  store.dispatch(setSelected(selection));
  thunkExtra.controller = {
    cancelReviewDiffRefresh: () => undefined,
    scheduleReviewDiffRefreshTimer: () => undefined,
  } as unknown as TerminalController | null;

  try {
    // Simulate the periodic silent refresh's own in-flight request.
    const polled = dispatch(loadReviewDiff({ silent: true }));
    await waitFor(() => pendingCalls.length === 1);

    // A manual "Refresh diff" click fires while that tick is still in flight.
    const clicked = dispatch(loadReviewDiff());

    // The click must wait for the in-flight request rather than starting a
    // second one immediately.
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(
      pendingCalls.length,
      1,
      'the click must wait for the in-flight request instead of racing it',
    );

    resolveCall(pendingCalls, 0, diffResult('stale'));
    await polled;

    // Only now should the click's forced refetch actually run.
    await waitFor(() => pendingCalls.length === 2);
    resolveCall(pendingCalls, 1, diffResult('fresh'));
    await clicked;

    assert.equal(
      store.getState().review.diffByEnv['acme/dev']?.diff?.rawDiff,
      'fresh',
      'the click must land its own fresh data, not the in-flight request’s stale result',
    );
  } finally {
    thunkExtra.controller = null;
  }
});
