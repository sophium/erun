// The one shared RTK Query endpoint definition erun#1283 asks for: written
// once here, injected into the console's own `createApi` over a real
// `httpBaseQuery` and, in the desktop's own test suite, over its real
// `wailsQueryFn`-shaped base query — proving the seam is real rather than two
// parallel stacks wearing one name. Nothing here calls `fetch` or knows about
// Wails; the transport is entirely the injected `BaseQuery`.
import type { BaseQueryFn, EndpointBuilder } from '@reduxjs/toolkit/query';

import { parseTenantConfigView, type TenantConfigView } from '../models/platformConfig';

// PlatformApiRequest is the abstract shape every transport's base query
// resolves. It intentionally mirrors RTK Query's own `fetchBaseQuery` args
// (`url`/`method`/`body`) so an HTTP transport needs no translation layer,
// while a non-HTTP transport (Wails IPC) just routes on `url`. `token` carries
// the bearer explicitly per request rather than through store state, matching
// every hand-rolled client this replaces (`fetchConfig(token)`, `createContext
// (token, input)`, …) so call sites barely change shape. `label` lets an
// endpoint keep its own "X request failed (status)" wording.
export interface PlatformApiRequest {
  url: string;
  method?: string;
  body?: unknown;
  token?: string;
  label?: string;
}

export interface PlatformApiError {
  message: string;
  status?: number;
}

export type PlatformBaseQuery = BaseQueryFn<PlatformApiRequest, unknown, PlatformApiError>;

// buildPlatformConfigEndpoints injects the shared `getConfig` endpoint into
// any `createApi` instance — the console wires it to a real httpBaseQuery;
// erun-ui/frontend's suite wires the same factory to a base query built from
// its own `wailsQueryFn`, both producing an identical TenantConfigView from
// identical fixture bytes. The injecting api's own tagTypes should include
// `'Config'` so a caller can invalidate this query after a write that changes
// what it reads (registering or deploying an environment, provisioning a
// context); the cast below trades cross-module tag-literal checking for
// letting any api's own TagTypes union inject this endpoint.
export function buildPlatformConfigEndpoints<TagTypes extends string, ReducerPath extends string>(
  builder: EndpointBuilder<PlatformBaseQuery, TagTypes, ReducerPath>,
) {
  return {
    getConfig: builder.query<TenantConfigView, string>({
      query: (token) => ({ url: '/v1/config', token, label: 'config request' }),
      transformResponse: (raw: unknown) => parseTenantConfigView(raw),
      providesTags: ['Config'] as unknown as TagTypes[],
    }),
  };
}
