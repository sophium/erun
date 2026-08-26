// Side-effect imports: cloudApi/tenantApi inject their endpoints into the
// shared wailsApi instance on import.
import './api/cloudApi';
import './api/tenantApi';

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';

import type { UITenant } from '@/types';

import { wailsApi } from './api/wailsApi';
import { loginPrimaryCloudProvider, signInAndRecover } from './cloudProviderThunks';
import { TENANT_IDENTITY_SIGN_IN_MESSAGE } from './platformSignIn';
import sidebarReducer from './slices/sidebarSlice';
import tenantDashboardReducer, { setTenantDashboard } from './slices/tenantDashboardSlice';
import tenantsReducer, { setTenants } from './slices/tenantsSlice';
import type { AppDispatch } from './store';
import { loadTenantDashboard } from './tenantDialogThunks';

// #1392: loginPrimaryCloudProvider logs in and updates the sidebar's alias
// state, but never re-fetched whatever surface had failed with a stale
// identity — a successful sign-in on the tenant-dashboard alert left the
// identical error and button on screen. #1390 shipped that alert with zero
// coverage of what happens after a *successful* login, because the
// Playwright harness's aws stub is inert and can never make a real login
// succeed. The seam that was missing is the RPC boundary itself
// (window.go.main.App.*, which every wailsQueryFn-backed endpoint calls
// through) — standing it up by hand here lets these tests drive a real
// success through the exact production dispatch path (cloudApi/tenantApi
// mutations and queries, real RTK Query middleware) rather than asserting
// dispatch shape and stopping there, which is exactly how #1390 shipped
// broken.
function stubWailsBridge(app: Record<string, (...args: never[]) => Promise<unknown>>): void {
  (globalThis as unknown as { window: unknown }).window = { go: { main: { App: app } } };
}

const ALIAS = 'frs-aws';
const TENANT: UITenant = {
  name: 'frs',
  defaultEnvironment: 'prod',
  primaryCloudProviderAlias: ALIAS,
  environments: [{ name: 'prod', apiUrl: 'https://api.frs.example' }],
};

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

function seedStaleIdentityDashboard(dispatch: AppDispatch): void {
  dispatch(setTenants([TENANT]));
  dispatch(
    setTenantDashboard({
      tenant: TENANT.name,
      tab: 'reviews',
      loading: false,
      error: '',
      data: {
        tenant: TENANT.name,
        apiError: TENANT_IDENTITY_SIGN_IN_MESSAGE,
        canCreateReview: false,
        canAdvanceMergeQueue: false,
      },
      reviewFilter: { mine: false, waitingOnMe: false },
    }),
  );
}

// This is the headline case: the panel must actually recover, not just clear
// a flag. A real (stubbed-at-the-RPC-boundary) successful login followed by
// a real dashboard re-fetch must land fresh, error-free data.
test('a successful sign-in from the tenant-dashboard identity alert re-fetches the dashboard and clears the stale error (#1392)', async () => {
  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  seedStaleIdentityDashboard(dispatch);

  stubWailsBridge({
    LoginCloudProvider: () => Promise.resolve({ alias: ALIAS, provider: 'aws', status: 'active' }),
    LoadTenantDashboard: () =>
      Promise.resolve({
        tenant: TENANT.name,
        canCreateReview: true,
        canAdvanceMergeQueue: true,
        reviews: [],
      }),
  });

  let recovered: Promise<void> | undefined;
  const signedIn = await dispatch(
    signInAndRecover(ALIAS, () => {
      recovered = dispatch(loadTenantDashboard());
    }),
  );
  assert.equal(signedIn, true, 'sign-in should report success once LoginCloudProvider resolves');
  await recovered;

  const dashboard = store.getState().tenantDashboard;
  assert.equal(
    dashboard.data?.apiError,
    undefined,
    'the stale identity error must clear once the dashboard has re-fetched',
  );
  assert.equal(
    dashboard.data?.canCreateReview,
    true,
    'the panel must show the freshly loaded dashboard, not a patched-over stale one',
  );
  assert.equal(dashboard.error, '');
});

// The mirror case (#1392's second requirement): a login that does not
// succeed must not silently re-render the identical message. No recovery
// runs, and the alert's own caller (signInAndRecover's return value) knows
// to say the sign-in itself failed.
test('a failed sign-in runs no recovery and leaves the error in place for the operator to retry', async () => {
  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  seedStaleIdentityDashboard(dispatch);

  stubWailsBridge({
    LoginCloudProvider: () => Promise.reject(new Error('access denied')),
  });

  let recoveredCalls = 0;
  const signedIn = await dispatch(
    signInAndRecover(ALIAS, () => {
      recoveredCalls += 1;
    }),
  );

  assert.equal(signedIn, false);
  assert.equal(recoveredCalls, 0, 'a failed sign-in must not run the recovery callback');
  assert.equal(
    store.getState().tenantDashboard.data?.apiError,
    TENANT_IDENTITY_SIGN_IN_MESSAGE,
    'the stale error must stay visible so the alert keeps offering Log in',
  );
});

// Regression guard: the sidebar's own cloud-alias login button dispatches
// loginPrimaryCloudProvider directly (no recovery callback) and must keep
// behaving exactly as before signInAndRecover was introduced.
test("the sidebar's own login thunk still logs in and updates cloud provider state", async () => {
  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  dispatch(setTenants([TENANT]));

  stubWailsBridge({
    LoginCloudProvider: () =>
      Promise.resolve({ alias: ALIAS, provider: 'aws', status: 'active', username: 'frs' }),
  });

  const signedIn = await dispatch(loginPrimaryCloudProvider(ALIAS));

  assert.equal(signedIn, true);
  const providers = store.getState().tenants.cloudProviders;
  assert.equal(providers.length, 1);
  assert.equal(providers[0]?.status, 'active');
  assert.equal(
    store.getState().sidebar.sidebarCloudAliasBusyByAlias[ALIAS] ?? '',
    '',
    'busy flag must clear after login settles',
  );
});
