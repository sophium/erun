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
import layoutReducer, { setReviewOpen } from './slices/layoutSlice';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';
import orchestratorsReducer, { setOrchestrators } from './slices/orchestratorsSlice';
import requestCountersReducer from './slices/requestCountersSlice';
import reviewReducer from './slices/reviewSlice';
import selectionReducer, { setSelected } from './slices/selectionSlice';
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
      contribute: contributeReducer,
      diffReviewStatus: diffReviewStatusReducer,
      layout: layoutReducer,
      orchestrators: orchestratorsReducer,
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

function orchestrator(sessionId: number): OrchestratorInfo {
  return {
    id: 'orch-1',
    name: 'orch-1',
    environments: [
      { tenant: 'acme', environment: 'alpha', directory: '/tmp/alpha', role: '' },
      { tenant: 'acme', environment: 'beta', directory: '/tmp/beta', role: '' },
    ],
    tenants: ['acme'],
    directories: ['/tmp/alpha', '/tmp/beta'],
    sessionId,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    nudgeCapped: false,
    autoNudgeCount: 0,
    whipCount: 0,
    restartRequired: false,
    roleChanged: false,
  };
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

// loadReviewDiff's own periodic
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

// scheduleReviewDiffRefresh's outer guard (checked once, before arming the
// timer) used to bail on `!state.selection.selected`, while the timer
// callback's own guard deliberately does not -- an orchestrator session has
// linked environments but no sidebar selection of its own. That mismatch left
// periodic review-diff refresh permanently dead for every orchestrator
// session: loadReviewDiff always calls scheduleReviewDiffRefresh at the end
// of its own fetch, so the outer guard ran on literally every load an
// orchestrator session ever made. Proves the fix behaviourally: the
// timer now arms (and fires) for an orchestrator session with no
// state.selection.selected.
test('scheduleReviewDiffRefresh arms its timer for an orchestrator session with no sidebar selection', async () => {
  const pendingCalls: Deferred[] = [];
  stubWailsBridge({
    LoadDiff: () =>
      new Promise<DiffResult>((resolve) => {
        pendingCalls.push({ resolve });
      }),
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  // An orchestrator session: sessionId matches an orchestrator, and the
  // sidebar's own selection is cleared -- exactly focusOrchestratorSession's
  // shape.
  store.dispatch(setOrchestrators([orchestrator(42)]));
  store.dispatch(setSessionId(42));
  store.dispatch(setSelected(null));
  store.dispatch(setReviewOpen(true));

  let scheduled = false;
  let scheduledCallback: (() => void) | null = null;
  thunkExtra.controller = {
    cancelReviewDiffRefresh: () => undefined,
    scheduleReviewDiffRefreshTimer: (callback: () => void) => {
      scheduled = true;
      scheduledCallback = callback;
    },
  } as unknown as TerminalController | null;

  try {
    const loaded = dispatch(loadReviewDiff());
    await waitFor(() => pendingCalls.length === 2);
    resolveCall(pendingCalls, 0, diffResult('alpha-diff'));
    resolveCall(pendingCalls, 1, diffResult('beta-diff'));
    await loaded;

    assert.ok(
      scheduled,
      'the periodic refresh timer must arm for an orchestrator session even though state.selection.selected is null',
    );
    assert.ok(scheduledCallback, 'a callback must have been captured');
  } finally {
    thunkExtra.controller = null;
  }
});
