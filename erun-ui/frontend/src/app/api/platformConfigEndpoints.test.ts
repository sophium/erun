import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';
import { createApi } from '@reduxjs/toolkit/query';
import { buildPlatformConfigEndpoints, type PlatformBaseQuery } from 'erun-kit';

import { wailsQueryFn } from './wailsBaseQuery';

// erun#1283's proof that the console's httpBaseQuery and the desktop's
// wailsBaseQuery contract resolve the identical shared endpoint definition
// (buildPlatformConfigEndpoints, defined once in erun-kit) to the identical
// model — see erun-kit/src/api/platformConfigEndpoints.test.ts for the
// http-shaped half of this proof. The desktop has no live Wails binding for
// this hosted-platform read today (its own Wails-exposed reads are the local
// erun config on disk, a different domain from the console's per-tenant
// provisioning view over erun-backend-api), so this wraps a fixture-returning
// async call the exact way every real desktop endpoint wraps its actual Go
// binding — see stateApi.ts/tenantApi.ts for the identical
// `wailsQueryFn(() => Go(...))` shape this test mirrors with a fixture in
// place of a real binding.

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
  tenant: { tenantId: 'tn-1', name: 'Acme', type: 'COMPANY', platformDeclaredName: undefined },
  environments: [
    {
      environmentId: 'env-1',
      name: 'prod',
      type: 'runtime',
      status: 'running',
      kubernetesContext: undefined,
      contextId: undefined,
      runtimeVersion: '1.2.3',
      provisionError: undefined,
      deployedVersion: undefined,
      deleteError: undefined,
    },
  ],
  contexts: [],
};

// wailsStyleBaseQuery is what a real desktop implementation would look like:
// a base query whose every request routes, by url, to wailsQueryFn wrapping
// a Wails-bound call. No fetch, no URL construction — exactly the fallback
// guard wailsBaseQuery.ts documents every real endpoint replacing with its
// own queryFn.
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
