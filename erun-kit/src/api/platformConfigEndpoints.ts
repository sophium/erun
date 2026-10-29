// Shared RTK Query endpoints; the transport is entirely the injected BaseQuery.
import type { BaseQueryFn, EndpointBuilder } from '@reduxjs/toolkit/query';

import { parseTenantConfigView, type TenantConfigView } from '../models/platformConfig';

// PlatformApiRequest mirrors fetchBaseQuery args so HTTP needs no translation; non-HTTP
// transports route on `url`. `token` is per request, not store state.
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
  // The backend's {code, message} envelope code, e.g. TENANT_UNRESOLVED vs NOT_ENROLLED,
  // which callers render differently.
  code?: string;
}

export type PlatformBaseQuery = BaseQueryFn<PlatformApiRequest, unknown, PlatformApiError>;

// buildPlatformConfigEndpoints injects `getConfig`; the injecting api's tagTypes should
// include 'Config'. The cast trades tag-literal checking for any api's TagTypes.
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
