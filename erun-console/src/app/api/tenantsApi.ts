// RTK Query endpoints for the operations-only /v1/tenants surface.
import { asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

// Set only by listTenants; undefined (not 0) elsewhere so an unloaded count never flags a tenant inert.
export interface PlatformTenant {
  tenantId: string;
  name: string;
  type: string;
  createdAt: string;
  updatedAt: string;
  userCount?: number;
  // Set only by getReachableTenants: membership is necessary but not sufficient for resolution.
  reachability?: TenantReachability;
  // Set only by listTenants; undefined means not computed.
  resolvable?: boolean;
}

// An open string: an unknown value reads as unreachable, and only RESOLVABLE counts as reachable.
export type TenantReachability = string;

export const TENANT_REACHABILITY_RESOLVABLE = 'RESOLVABLE';
export const TENANT_REACHABILITY_ORG_MISMATCH = 'ORG_MISMATCH';
export const TENANT_REACHABILITY_NO_ORG_MAPPING = 'NO_ORG_MAPPING';
export const TENANT_REACHABILITY_ISSUER_NOT_MAPPED = 'ISSUER_NOT_MAPPED';

function asOptionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' ? value : undefined;
}

function asOptionalBoolean(value: unknown): boolean | undefined {
  return typeof value === 'boolean' ? value : undefined;
}

function asOptionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

function parsePlatformTenant(raw: Record<string, unknown>): PlatformTenant {
  return {
    tenantId: asString(raw.tenantId),
    name: asString(raw.name),
    type: asString(raw.type),
    createdAt: asString(raw.createdAt),
    updatedAt: asString(raw.updatedAt),
    userCount: asOptionalNumber(raw.userCount),
    reachability: asOptionalString(raw.reachability),
    resolvable: asOptionalBoolean(raw.resolvable),
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

// orgFieldKey/orgFieldValue are set only for an org-scoped (shared) issuer.
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

    // Available to any signed-in caller; backs the tenant switcher.
    getReachableTenants: builder.query<PlatformTenant[], string>({
      query: (token) => ({ url: '/v1/tenants/reachable', token, label: 'list reachable tenants' }),
      transformResponse: parsePlatformTenantList,
    }),
  }),
});

export const { useListTenantsQuery, useCreateTenantMutation, useGetReachableTenantsQuery } =
  tenantsApi;
