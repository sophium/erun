import assert from 'node:assert/strict';

import { configureStore } from '@reduxjs/toolkit';
import { createApi } from '@reduxjs/toolkit/query';
import { buildPlatformConfigEndpoints, type PlatformBaseQuery } from 'erun-kit';
import { describe, it } from 'vitest';

import { wailsQueryFn } from './wailsBaseQuery';

// The shared endpoint definition resolves to the same model through a
// Wails-style base query as through the console's http one (see the erun-kit test).

const FIXTURE_BODY = {
  tenant: { tenantId: 'tn-1', name: 'Acme', type: 'COMPANY' },
  environments: [
    {
      environmentId: 'env-1',
      name: 'prod',
      type: 'runtime',
      status: 'running',
      runtimeVersion: '1.2.3',
    },
  ],
  contexts: [],
};

const EXPECTED = {
  tenant: { tenantId: 'tn-1', name: 'Acme', type: 'COMPANY' },
  environments: [
    {
      environmentId: 'env-1',
      name: 'prod',
      type: 'runtime',
      status: 'running',
      // Absent from the fixture, so the lenient parse leaves it undefined.
      tenantId: undefined,
      kubernetesContext: undefined,
      contextId: undefined,
      runtimeVersion: '1.2.3',
      provisionError: undefined,
      deployedVersion: undefined,
      deleteError: undefined,
      exposedHostname: undefined,
    },
  ],
  contexts: [],
  inviteRequestRateLimitWindowSeconds: 0,
};

// Routes each request to wailsQueryFn, as a real desktop endpoint would.
const wailsStyleBaseQuery: PlatformBaseQuery = (request) => {
  if (request.url !== '/v1/config') {
    return Promise.resolve({ error: { message: `unexpected request: ${request.url}` } });
  }
  return wailsQueryFn(() => Promise.resolve(FIXTURE_BODY))(undefined);
};

describe('buildPlatformConfigEndpoints over a wailsQueryFn-backed base query', () => {
  it('resolves the identical TenantConfigView the console httpBaseQuery would', async () => {
    const api = createApi({
      reducerPath: 'platformConfigOverWails',
      baseQuery: wailsStyleBaseQuery,
      tagTypes: ['Config'],
      endpoints: (builder) => buildPlatformConfigEndpoints(builder),
    });
    const store = configureStore({
      reducer: { [api.reducerPath]: api.reducer },
      middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(api.middleware),
    });

    const result = await store.dispatch(api.endpoints.getConfig.initiate('tok-1'));

    assert.deepStrictEqual(result.data, EXPECTED);
  });
});
