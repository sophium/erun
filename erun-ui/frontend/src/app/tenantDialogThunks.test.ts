// Side-effect imports: these apis inject their endpoints into the shared
// wailsApi instance on import.
import './api/tenantApi';
import './api/tenantInviteRequestApi';
import './api/cloudApi';

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';

import type { UITenantDashboard } from '@/types';

import { wailsApi } from './api/wailsApi';
import { defaultReviewFilter, defaultTenantDashboard } from './reviewDetailState';
import tenantDashboardReducer from './slices/tenantDashboardSlice';
import { setTenantDashboard } from './slices/tenantDashboardSlice';
import tenantsReducer from './slices/tenantsSlice';
import type { AppDispatch } from './store';
import { loadTenantDashboard } from './tenantDialogThunks';

// Mirrors bootThunks.test.ts's RPC-boundary stub.
function stubWailsBridge(app: Record<string, (...args: never[]) => Promise<unknown>>): void {
  (globalThis as unknown as { window: unknown }).window = { go: { main: { App: app } } };
}

function buildTestStore() {
  return configureStore({
    reducer: {
      tenantDashboard: tenantDashboardReducer,
      tenants: tenantsReducer,
      [wailsApi.reducerPath]: wailsApi.reducer,
    },
    middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(wailsApi.middleware),
  });
}

interface Deferred {
  resolve: (value: UITenantDashboard) => void;
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

function resolveCall(calls: Deferred[], index: number, dashboard: UITenantDashboard): void {
  const call = calls[index];
  assert.ok(call, `expected a pending call at index ${String(index)}`);
  call.resolve(dashboard);
}

function dashboard(platformState: string): UITenantDashboard {
  return { platformState } as UITenantDashboard;
}

// erun#1981 (audit follow-up to #1953/#1976): loadTenantDashboard has many
// independent callers for the same tenant -- dialog open, the Refresh click,
// and the post-mutation reload every registration/platform-connect/invite-
// request thunk runs -- with no periodic poll to catch a dropped one later.
// RTK Query's condition() check bails a forced refetch out from under a
// pending request for the same query before ever looking at forceRefetch,
// so a later caller landing while an earlier one is still in flight used to
// silently inherit that request's pre-mutation data. This proves the fix
// behaviourally: a load triggered mid-flight still ends up with a second,
// real fetch and fresh data, not the first fetch's stale result.
test('a dashboard load triggered during an in-flight getTenantDashboard still produces a fresh fetch', async () => {
  const pendingCalls: Deferred[] = [];
  stubWailsBridge({
    LoadTenantDashboard: () =>
      new Promise<UITenantDashboard>((resolve) => {
        pendingCalls.push({ resolve });
      }),
  });

  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  store.dispatch(
    setTenantDashboard({
      ...defaultTenantDashboard(),
      tenant: 'acme',
      reviewFilter: defaultReviewFilter(),
    }),
  );

  // Simulate the dashboard's own open load.
  const opened = dispatch(loadTenantDashboard('acme'));
  await waitFor(() => pendingCalls.length === 1);

  // A post-mutation reload (e.g. after approving an invite request) fires
  // while that load is still in flight.
  const reloaded = dispatch(loadTenantDashboard('acme'));

  // The reload must wait for the in-flight request rather than starting a
  // second one immediately.
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(
    pendingCalls.length,
    1,
    'the reload must wait for the in-flight request instead of racing it',
  );

  resolveCall(pendingCalls, 0, dashboard('stale'));
  await opened;

  // Only now should the reload's forced refetch actually run.
  await waitFor(() => pendingCalls.length === 2);
  resolveCall(pendingCalls, 1, dashboard('fresh'));
  await reloaded;

  assert.equal(
    store.getState().tenantDashboard.data?.platformState,
    'fresh',
    'the reload must land its own fresh data, not the in-flight request’s stale result',
  );
});
