// Side-effect imports: each api registers its endpoints on the shared wailsApi.
import './api/cloudApi';
import './api/tenantApi';
import './api/tenantInviteRequestApi';

import assert from 'node:assert/strict';

import { configureStore } from '@reduxjs/toolkit';
import { test } from 'vitest';

import { TENANT_PLATFORM_STATE_NOT_SIGNED_IN } from '@/types';

import { tenantInviteRequestApi } from './api/tenantInviteRequestApi';
import { wailsApi } from './api/wailsApi';
import { loginPrimaryCloudProvider, signInAndRecover } from './cloudProviderThunks';
import sidebarReducer, { setSidebarCloudAliasBusy } from './slices/sidebarSlice';
import tenantDashboardReducer, { setTenantDashboard } from './slices/tenantDashboardSlice';
import tenantsReducer from './slices/tenantsSlice';
import type { AppDispatch } from './store';
import { loadTenantDashboard } from './tenantDialogThunks';
import { defaultRegistrationState } from './tenantRegistrationState';

// Stubs the RPC boundary so a real login success runs the production dispatch path.
function stubWailsBridge(app: Record<string, (...args: never[]) => Promise<unknown>>): void {
  (globalThis as unknown as { window: unknown }).window = { go: { main: { App: app } } };
}

const TENANT = 'frs';
const ALIAS = 'erun+api.frs-prod.services.erunpaas.com@erun';

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
        canOverrideMergeQueue: false,
        canCreateContext: false,
        canRegisterEnvironment: false,
        canDeployEnvironment: false,
        canStopEnvironment: false,
        canDeleteEnvironment: false,
        canApproveInviteRequests: false,
        canDeclineInviteRequests: false,
      },
      reviewFilter: { mine: false, waitingOnMe: false, statuses: ['OPEN', 'MERGE'] },
      platformAliasOverride: '',
      connectApiUrlDraft: '',
      connecting: false,
      connectError: '',
      enrollUsernameDraft: '',
      enrolling: false,
      enrollError: '',
      registration: defaultRegistrationState(),
      requestDialogOpen: false,
      requestKindDraft: 'JOIN_TENANT',
      requestNoteDraft: '',
      requesting: false,
      requestError: '',
      requestRateLimitedUntil: 0,
      decliningInviteRequestId: '',
      declineReasonDraft: '',
      decidingInviteRequestId: '',
      decideInviteRequestError: '',
      issuedInviteLink: null,
    }),
  );
}

// Polls: invalidateTags refetches in the background with no promise to await.
async function waitFor(predicate: () => boolean, timeoutMs = 1000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!predicate()) {
    if (Date.now() > deadline) {
      throw new Error('timed out waiting for condition');
    }
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}

function enrollmentStatus(store: ReturnType<typeof buildTestStore>): string | undefined {
  return tenantInviteRequestApi.endpoints.listTenantPlatformEnrollmentStatuses.select({
    tenants: [TENANT],
  })(store.getState()).data?.[0]?.state;
}

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

// The query must be subscribed: invalidateTags never refetches an unsubscribed one.
test('a successful sign-in invalidates the sidebar tenant-enrollment status query, turning local-only into enrolled without a restart (#1824)', async () => {
  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;

  let enrolled = false;
  stubWailsBridge({
    LoginCloudProvider: () => Promise.resolve({ alias: ALIAS, provider: 'erun', status: 'active' }),
    ListTenantPlatformEnrollmentStatuses: () =>
      Promise.resolve([{ tenant: TENANT, state: enrolled ? 'enrolled' : 'local-only' }]),
  });

  const subscription = dispatch(
    tenantInviteRequestApi.endpoints.listTenantPlatformEnrollmentStatuses.initiate({
      tenants: [TENANT],
    }),
  );
  const initial = await subscription;
  assert.equal(initial.data?.[0]?.state, 'local-only');

  enrolled = true;
  const outcome = await dispatch(loginPrimaryCloudProvider(ALIAS));
  assert.equal(outcome.status, 'success');

  await waitFor(() => enrollmentStatus(store) === 'enrolled');

  subscription.unsubscribe();
});

test("loading the tenant dashboard and resolving a whoami also invalidates the sidebar's enrollment status query", async () => {
  const store = buildTestStore();
  const dispatch = store.dispatch as unknown as AppDispatch;
  seedNotSignedInDashboard(dispatch);

  let enrolled = false;
  stubWailsBridge({
    LoadTenantDashboard: () =>
      Promise.resolve({
        tenant: TENANT,
        platformState: '',
        platformAlias: ALIAS,
        canCreateReview: true,
        canAdvanceMergeQueue: true,
        reviews: [],
      }),
    ListTenantPlatformEnrollmentStatuses: () =>
      Promise.resolve([{ tenant: TENANT, state: enrolled ? 'enrolled' : 'local-only' }]),
  });

  const subscription = dispatch(
    tenantInviteRequestApi.endpoints.listTenantPlatformEnrollmentStatuses.initiate({
      tenants: [TENANT],
    }),
  );
  await subscription;

  enrolled = true;
  await dispatch(loadTenantDashboard());

  await waitFor(() => enrollmentStatus(store) === 'enrolled');

  subscription.unsubscribe();
});
