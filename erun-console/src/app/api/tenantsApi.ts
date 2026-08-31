// RTK Query endpoints for /v1/tenants: the operations-only tenant-registration
// surface. The full erun-backend-api record — unlike
// erun-kit's `Tenant` (the `GET /v1/config` read model, just tenantId/name/
// type for the caller's own tenant) — also carries createdAt/updatedAt, so it
// gets its own local shape rather than reusing the kit one.
import { asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

// userCount is populated only by listTenants (the operations-only listing
// erun-backend-api's TenantRepository.List computes it for); it is
// `undefined` — not 0 — for the createTenant/getReachableTenants responses,
// which never count. Collapsing "not computed" into 0 would flag a healthy
// tenant as inert the moment its count simply hadn't loaded yet.
export interface PlatformTenant {
  tenantId: string;
  name: string;
  type: string;
  createdAt: string;
  updatedAt: string;
  userCount?: number;
}

function asOptionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' ? value : undefined;
}

function parsePlatformTenant(raw: Record<string, unknown>): PlatformTenant {
  return {
    tenantId: asString(raw.tenantId),
    name: asString(raw.name),
    type: asString(raw.type),
    createdAt: asString(raw.createdAt),
    updatedAt: asString(raw.updatedAt),
    userCount: asOptionalNumber(raw.userCount),
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

    // getReachableTenants answers "which tenants can I, this caller, reach" —
    // GET /v1/tenants/reachable, available to any signed-in caller (unlike
    // listTenants above, which is operations-only). It backs the tenant
    // switcher: the caller's own identity may map to more than one tenant.
    getReachableTenants: builder.query<PlatformTenant[], string>({
      query: (token) => ({ url: '/v1/tenants/reachable', token, label: 'list reachable tenants' }),
      transformResponse: parsePlatformTenantList,
    }),
  }),
});

export const { useListTenantsQuery, useCreateTenantMutation, useGetReachableTenantsQuery } =
  tenantsApi;
