import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';
import { createApi } from '@reduxjs/toolkit/query';

import type { TenantConfigView } from '../models/platformConfig';
import { buildPlatformConfigEndpoints, type PlatformBaseQuery } from './platformConfigEndpoints';

// This is the erun#1283 proof that the seam is real: the SAME endpoint
// definition (buildPlatformConfigEndpoints), injected into two independent
// createApi instances with two independently-implemented base queries — one
// shaped like a real HTTP fetch, one shaped like erun-ui/frontend's real
// wailsQueryFn contract (an arbitrary async call wrapped into
// `{data} | {error}`) — resolves to byte-identical TenantConfigView output
// from the same fixture. erun-ui/frontend's own suite runs the equivalent
// test using its real wailsQueryFn helper directly; see
// src/app/api/platformConfigEndpoints.test.ts there.

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
  contexts: [
    {
      contextId: 'ctx-1',
      name: 'primary',
      provider: 'aws',
      region: 'eu-west-2',
      status: 'running',
    },
  ],
};

const EXPECTED: TenantConfigView = {
  tenant: { tenantId: 'tn-1', name: 'Acme', type: 'COMPANY' },
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
  contexts: [
    {
      contextId: 'ctx-1',
      name: 'primary',
      provider: 'aws',
      region: 'eu-west-2',
      status: 'running',
      kubernetesContext: undefined,
      cloudProviderAlias: undefined,
      instanceType: undefined,
      provisionError: undefined,
    },
  ],
  inviteRequestRateLimitWindowSeconds: 0,
};

function buildTestApi(reducerPath: string, baseQuery: PlatformBaseQuery) {
  return createApi({
    reducerPath,
    baseQuery,
    tagTypes: ['Config'],
    endpoints: (builder) => buildPlatformConfigEndpoints(builder),
  });
}

async function resolveConfig(baseQuery: PlatformBaseQuery, reducerPath: string): Promise<unknown> {
  const api = buildTestApi(reducerPath, baseQuery);
  const store = configureStore({
    reducer: { [api.reducerPath]: api.reducer },
    middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(api.middleware),
  });
  const result = await store.dispatch(api.endpoints.getConfig.initiate('tok-1'));
  return result.data;
}

describe('buildPlatformConfigEndpoints', () => {
  it('resolves over an http-shaped base query', async () => {
    const httpLikeBaseQuery: PlatformBaseQuery = (request) => {
      assert.equal(request.url, '/v1/config');
      assert.equal(request.token, 'tok-1');
      return Promise.resolve({ data: FIXTURE_BODY });
    };
    const data = await resolveConfig(httpLikeBaseQuery, 'httpLikeApi');
    assert.deepStrictEqual(data, EXPECTED);
  });

  it('resolves the identical model over a wails-shaped base query', async () => {
    // Mirrors erun-ui/frontend's real wailsQueryFn: any endpoint just wraps an
    // arbitrary async call into `{data} | {error}` — no URL, no fetch.
    const wailsLikeBaseQuery: PlatformBaseQuery = async (request) => {
      if (request.url !== '/v1/config') {
        return { error: { message: 'unknown request' } };
      }
      try {
        return { data: await Promise.resolve(FIXTURE_BODY) };
      } catch (error) {
        return { error: { message: error instanceof Error ? error.message : String(error) } };
      }
    };
    const data = await resolveConfig(wailsLikeBaseQuery, 'wailsLikeApi');
    assert.deepStrictEqual(data, EXPECTED);
  });
});
