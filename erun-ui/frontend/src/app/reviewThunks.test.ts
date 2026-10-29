// Side-effect import: registers reviewApi's endpoints on wailsApi.
import './api/reviewApi';

import assert from 'node:assert/strict';

import { configureStore } from '@reduxjs/toolkit';
import { test } from 'vitest';

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
    launchSettingsChanged: false,
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
    const polled = dispatch(loadReviewDiff({ silent: true }));
    await waitFor(() => pendingCalls.length === 1);

    const clicked = dispatch(loadReviewDiff());

    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(
      pendingCalls.length,
      1,
      'the click must wait for the in-flight request instead of racing it',
    );

    resolveCall(pendingCalls, 0, diffResult('stale'));
    await polled;

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
  // Orchestrator session with no sidebar selection.
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
