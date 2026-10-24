// Side-effect imports register endpoints on wailsApi.
import './api/tenantApi';
import './api/cloudApi';
import './api/tenantInviteRequestApi';

import assert from 'node:assert/strict';

import { configureStore } from '@reduxjs/toolkit';
import { test } from 'vitest';

import type { UICloudProviderStatus, UITenantDashboard } from '@/types';

import { wailsApi } from './api/wailsApi';
import { defaultReviewFilter, defaultTenantDashboard } from './reviewDetailState';
import sidebarReducer from './slices/sidebarSlice';
import tenantDashboardReducer, { setTenantDashboard } from './slices/tenantDashboardSlice';
import tenantsReducer from './slices/tenantsSlice';
import type { AppDispatch } from './store';
import { connectTenantPlatform } from './tenantPlatformConnectThunks';

function stubWailsBridge(app: Record<string, (...args: never[]) => Promise<unknown>>): void {
  (globalThis as unknown as { window: unknown }).window = { go: { main: { App: app } } };
}

function buildTestStore() {
  return configureStore({
    reducer: {
      sidebar: sidebarReducer,
      tenantDashboard: tenantDashboardReducer,
      tenants: tenantsReducer,
      [wailsApi.reducerPath]: wailsApi.reducer,
    },
    middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(wailsApi.middleware),
  });
}

const ALIAS = 'erun+api.erunpaas.com@erun';
const API_URL = 'https://api.erunpaas.com';

function attachedAlias(): UICloudProviderStatus {
  return { alias: ALIAS, provider: 'erun', status: 'active' };
}

function seedDashboard(store: ReturnType<typeof buildTestStore>, tenant: string): void {
  store.dispatch(
    setTenantDashboard({
      ...defaultTenantDashboard(),
      tenant,
      reviewFilter: defaultReviewFilter(),
      connectApiUrlDraft: API_URL,
    }),
  );
}

// A skipped grant sets no error; a failed one carries the grant's reason.
test('a failed sign-in after a successful attach is reported on the card, not swallowed', async () => {
  stubWailsBridge({
    ConnectERunPlatform: () => Promise.resolve(attachedAlias()),
    LoginCloudProvider: () => Promise.reject(new Error('the grant was refused')),
  });

  const store = buildTestStore();
  seedDashboard(store, 'erun');
  await (store.dispatch as unknown as AppDispatch)(connectTenantPlatform(API_URL));

  const state = store.getState().tenantDashboard;
  assert.equal(state.connecting, false, 'the click must not stay in its connecting state');
  assert.match(
    state.connectError,
    /signing in failed/,
    'a failed sign-in must be visible on the card rather than leaving it unchanged',
  );
  assert.match(state.connectError, /the grant was refused/, 'the grant’s own reason must survive');
});

test('the click sends the tenant it was clicked from, so the attach lands where the dashboard reads it', async () => {
  const seen: { apiUrl?: string; tenant?: string }[] = [];
  stubWailsBridge({
    ConnectERunPlatform: (input: unknown) => {
      seen.push(input as { apiUrl?: string; tenant?: string });
      return Promise.resolve(attachedAlias());
    },
    LoginCloudProvider: () => Promise.resolve(attachedAlias()),
    LoadTenantDashboard: () =>
      Promise.resolve({ platformState: 'not-signed-in' } as UITenantDashboard),
  });

  const store = buildTestStore();
  seedDashboard(store, 'erun');
  await (store.dispatch as unknown as AppDispatch)(connectTenantPlatform(API_URL));

  assert.equal(seen.length, 1, 'expected exactly one connect call');
  const sent = seen[0] as { apiUrl?: string; tenant?: string };
  assert.equal(sent.tenant, 'erun', 'the clicked tenant must travel with the attach');
  assert.equal(sent.apiUrl, API_URL);

  const state = store.getState().tenantDashboard;
  assert.equal(state.connectError, '');
  assert.equal(state.connecting, false);
});
