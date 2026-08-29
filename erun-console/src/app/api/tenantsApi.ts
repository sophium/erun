// RTK Query endpoints for /v1/tenants: the operations-only tenant-registration
// surface. The full erun-backend-api record — unlike
// erun-kit's `Tenant` (the `GET /v1/config` read model, just tenantId/name/
// type for the caller's own tenant) — also carries createdAt/updatedAt, so it
// gets its own local shape rather than reusing the kit one.
import { asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

export interface PlatformTenant {
  tenantId: string;
  name: string;
  type: string;
  createdAt: string;
  updatedAt: string;
}

function parsePlatformTenant(raw: Record<string, unknown>): PlatformTenant {
  return {
    tenantId: asString(raw.tenantId),
    name: asString(raw.name),
    type: asString(raw.type),
    createdAt: asString(raw.createdAt),
    updatedAt: asString(raw.updatedAt),
  };
}

function parsePlatformTenantList(value: unknown): PlatformTenant[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parsePlatformTenant);
}

function parsePlatformTenantResponse(raw: unknown): PlatformTenant {
  if (!isRecord(raw)) {
    throw new Error('create tenant response was not in the expected shape');
  }
  return parsePlatformTenant(raw);
}

// CreateTenantInput mirrors POST /v1/tenants' body (see
// erun-docs/docs/agent-reference/api-protocol.md#post-v1tenants):
// orgFieldKey/orgFieldValue are set only for an org-scoped (shared) issuer,
// and left undefined for a single-tenant issuer.
export interface CreateTenantInput {
  name: string;
  type: string;
  issuer: string;
  orgFieldKey?: string;
  orgFieldValue?: string;
  displayName?: string;
}

export const tenantsApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    listTenants: builder.query<PlatformTenant[], string>({
      query: (token) => ({ url: '/v1/tenants', token, label: 'list tenants' }),
      transformResponse: parsePlatformTenantList,
      providesTags: ['Tenants'],
    }),

    createTenant: builder.mutation<PlatformTenant, { token: string; input: CreateTenantInput }>({
      query: ({ token, input }) => ({
        url: '/v1/tenants',
        method: 'POST',
        body: input,
        token,
        label: 'create tenant',
      }),
      transformResponse: parsePlatformTenantResponse,
      invalidatesTags: ['Tenants'],
    }),
  }),
});

export const { useListTenantsQuery, useCreateTenantMutation } = tenantsApi;
