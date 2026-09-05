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
  // reachability is populated only by getReachableTenants: whether a sign-in
  // by this caller's own identity can actually resolve to this tenant.
  // Membership and resolution use different keys, so a membership row is
  // necessary but not sufficient — see TenantReachability.
  reachability?: TenantReachability;
  // resolvable is populated only by listTenants: whether any of this tenant's
  // issuer mappings can resolve a token at all, for anyone. `undefined` means
  // the read never computed it, never "assume it works".
  resolvable?: boolean;
}

// TenantReachability mirrors erun-backend-api's model.TenantReachability. It
// stays a plain string rather than a closed union: a value this console does
// not know must read as "unreachable, reason unknown" rather than be coerced
// into one of the reasons it does know, and only RESOLVABLE may ever be
// treated as reachable.
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
