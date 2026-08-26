// Side-effect imports: cloudApi/tenantApi inject their endpoints into the
// shared wailsApi instance on import.
import './api/cloudApi';
import './api/tenantApi';

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';

import { TENANT_PLATFORM_STATE_NOT_SIGNED_IN } from '@/types';

import { wailsApi } from './api/wailsApi';
import { loginPrimaryCloudProvider, signInAndRecover } from './cloudProviderThunks';
import sidebarReducer, { setSidebarCloudAliasBusy } from './slices/sidebarSlice';
import tenantDashboardReducer, { setTenantDashboard } from './slices/tenantDashboardSlice';
import tenantsReducer from './slices/tenantsSlice';
import type { AppDispatch } from './store';
import { loadTenantDashboard } from './tenantDialogThunks';

// #1392/#1393: loginPrimaryCloudProvider logs in and updates the sidebar's
// alias state, but never re-fetched whatever surface had failed with a stale
// identity — a successful sign-in on the tenant-dashboard's not-signed-in
// state left the identical state and button on screen. #1390 shipped that
// alert with zero coverage of what happens after a *successful* login,
// because the Playwright harness's aws stub is inert and can never make a
// real login succeed. The seam that was missing is the RPC boundary itself
// (window.go.main.App.*, which every wailsQueryFn-backed endpoint calls
// through) — standing it up by hand here lets these tests drive a real
// success through the exact production dispatch path (cloudApi/tenantApi
// mutations and queries, real RTK Query middleware) rather than asserting
// dispatch shape and stopping there, which is exactly how #1390 shipped
// broken.
function stubWailsBridge(app: Record<string, (...args: never[]) => Promise<unknown>>): void {
  (globalThis as unknown as { window: unknown }).window = { go: { main: { App: app } } };
}

const TENANT = 'frs';
const ALIAS = 'erun+api.frs-prod.services.erunpaas.com@erun';

// A minimal store carrying only the slices these thunks actually touch, plus
// the real wailsApi reducer/middleware so RTK Query's mutation/query
// lifecycle (including .unwrap()) runs exactly as it does in the app.
function buildTestStore() {
  return configureStore({
    reducer: {
      sidebar: sidebarReducer,
      tenants: tenantsReducer,
      tenantDashboard: tenantDashboardReducer,
      [wailsApi.reducerPath]: wailsApi.reducer,
    },
    middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(wailsApi.middleware),
  });
}

function seedNotSignedInDashboard(dispatch: AppDispatch): void {
  dispatch(
    setTenantDashboard({
      tenant: TENANT,
      tab: 'reviews',
      loading: false,
      error: '',
      data: {
        tenant: TENANT,
        platformState: TENANT_PLATFORM_STATE_NOT_SIGNED_IN,
        platformAlias: ALIAS,
        canCreateReview: false,
        canAdvanceMergeQueue: false,
      },
      reviewFilter: { mine: false, waitingOnMe: false },
      platformAliasOverride: '',
      connectApiUrlDraft: '',
      connecting: false,
      connectError: '',
      enrollUsernameDraft: '',
      enrolling: false,
      enrollError: '',
    }),
  );
}

// This is the headline case: the panel must actually recover, not just clear
// a flag. A real (stubbed-at-the-RPC-boundary) successful login followed by
// a real dashboard re-fetch must land fresh, error-free data.
test('a successful sign-in from the tenant dashboard’s not-signed-in state re-fetches the dashboard and clears it (#1392, #1393)', async () => {
  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  seedNotSignedInDashboard(dispatch);

  stubWailsBridge({
    LoginCloudProvider: () => Promise.resolve({ alias: ALIAS, provider: 'erun', status: 'active' }),
    LoadTenantDashboard: () =>
      Promise.resolve({
        tenant: TENANT,
        platformState: '',
        platformAlias: ALIAS,
        canCreateReview: true,
        canAdvanceMergeQueue: true,
        reviews: [],
      }),
  });

  let recovered: Promise<void> | undefined;
  const outcome = await dispatch(
    signInAndRecover(ALIAS, () => {
      recovered = dispatch(loadTenantDashboard());
    }),
  );
  assert.equal(
    outcome.status,
    'success',
    'sign-in should report success once LoginCloudProvider resolves',
  );
  await recovered;

  const dashboard = store.getState().tenantDashboard;
  assert.equal(
    dashboard.data?.platformState,
    '',
    'the not-signed-in state must clear once the dashboard has re-fetched',
  );
  assert.equal(
    dashboard.data.canCreateReview,
    true,
    'the panel must show the freshly loaded dashboard, not a patched-over stale one',
  );
  assert.equal(dashboard.error, '');
});

// The mirror case (#1392's second requirement, review defect 1): a login
// that does not succeed must not silently re-render the identical state, and
// the real failure reason — not a generic "Sign-in failed. Try again." —
// must be what the caller learns.
test('a failed sign-in runs no recovery and reports the real reason, not a generic message', async () => {
  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  seedNotSignedInDashboard(dispatch);

  stubWailsBridge({
    LoginCloudProvider: () => Promise.reject(new Error('device code expired')),
  });

  let recoveredCalls = 0;
  const outcome = await dispatch(
    signInAndRecover(ALIAS, () => {
      recoveredCalls += 1;
    }),
  );

  assert.equal(outcome.status, 'failed');
  assert.equal(
    outcome.message,
    'device code expired',
    'the caller must learn the real failure reason, not a generic sentence',
  );
  assert.equal(recoveredCalls, 0, 'a failed sign-in must not run the recovery callback');
  assert.equal(
    store.getState().tenantDashboard.data?.platformState,
    TENANT_PLATFORM_STATE_NOT_SIGNED_IN,
    'the not-signed-in state must stay visible so the operator can retry',
  );
});

// #1392 review, second defect: a click while the alias is already busy with
// another attempt must not run at all — and must not report "failed" for an
// attempt that never happened.
test('signing in while the alias is already busy is reported as skipped, not failed', async () => {
  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  seedNotSignedInDashboard(dispatch);
  dispatch(setSidebarCloudAliasBusy({ alias: ALIAS, busy: true, action: 'login' }));

  stubWailsBridge({
    LoginCloudProvider: () => {
      throw new Error('must not be called while already busy');
    },
  });

  let recoveredCalls = 0;
  const outcome = await dispatch(
    signInAndRecover(ALIAS, () => {
      recoveredCalls += 1;
    }),
  );

  assert.equal(outcome.status, 'skipped');
  assert.equal(recoveredCalls, 0);
});

// Regression guard: the sidebar's own cloud-alias login button dispatches
// loginPrimaryCloudProvider directly (no recovery callback) and must keep
// behaving exactly as before signInAndRecover was introduced.
test("the sidebar's own login thunk still logs in and updates cloud provider state", async () => {
  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;

  stubWailsBridge({
    LoginCloudProvider: () =>
      Promise.resolve({ alias: ALIAS, provider: 'erun', status: 'active', username: 'erun' }),
  });

  const outcome = await dispatch(loginPrimaryCloudProvider(ALIAS));

  assert.equal(outcome.status, 'success');
  const providers = store.getState().tenants.cloudProviders;
  assert.equal(providers.length, 1);
  assert.equal(providers[0]?.status, 'active');
  assert.equal(
    store.getState().sidebar.sidebarCloudAliasBusyByAlias[ALIAS] ?? '',
    '',
    'busy flag must clear after login settles',
  );
});
