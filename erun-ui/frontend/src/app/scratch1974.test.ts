import './api/stateApi';

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';

import type { UIState, UITenant } from '@/types';

import { stateApi } from './api/stateApi';
import { wailsApi } from './api/wailsApi';
import { reloadStateAfterEnvironmentChange } from './bootThunks';
import tenantsReducer from './slices/tenantsSlice';
import type { AppDispatch } from './store';

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

test('two independent callers racing into reloadStateAfterEnvironmentChange while a prior fetch is in flight', async () => {
  const pendingCalls: Deferred[] = [];
  stubWailsBridge({
    LoadState: () =>
      new Promise<UIState>((resolve) => {
        pendingCalls.push({ resolve });
      }),
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;

  // Simulate an original in-flight load (e.g. boot()'s own fetch).
  const inFlight = dispatch(stateApi.endpoints.getInitialState.initiate(undefined)).unwrap();
  await waitFor(() => pendingCalls.length === 1);

  // Two independent callers race in while that load is still pending —
  // dispatched back-to-back, synchronously, like two near-simultaneous
  // Wails event handlers.
  const callerA = dispatch(reloadStateAfterEnvironmentChange());
  const callerB = dispatch(reloadStateAfterEnvironmentChange());

  resolveCall(pendingCalls, 0, { tenants: [tenant('stale')] });
  await inFlight;

  // Both callers should now each trigger their own genuine forced refetch.
  await waitFor(() => pendingCalls.length >= 2, 2000).catch(() => undefined);
  console.log('pendingCalls.length after both callers resumed:', pendingCalls.length);

  // Drain whatever shows up.
  for (let i = 1; i < pendingCalls.length; i++) {
    resolveCall(pendingCalls, i, { tenants: [tenant(`fresh-${String(i)}`)] });
  }
  await Promise.allSettled([callerA, callerB]);
  console.log('final pendingCalls.length:', pendingCalls.length);
  console.log('final tenants:', store.getState().tenants.tenants.map((t) => t.name));
});
