// RTK Query endpoints for the tenant quota surface: GET /v1/quota is every
// tenant's self-service read of its own caps (env count plus the
// per-environment and aggregate tenant-wide resource ceilings); PUT
// /v1/tenants/{tenant_id}/quota is the operations-only cross-tenant write
// that sets another tenant's caps (see erun-backend-api/internal/routes/
// tenant_quotas.go).
import { asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

export interface TenantQuota {
  tenantId: string;
  maxEnvironments: number;
  maxCpuMillicores: number;
  maxMemoryMb: number;
  maxStorageGb: number;
  maxTotalCpuMillicores: number;
  maxTotalMemoryMb: number;
  maxTotalStorageGb: number;
}

function asNumber(value: unknown): number {
  return typeof value === 'number' ? value : 0;
}

function parseTenantQuota(raw: unknown): TenantQuota {
  if (!isRecord(raw)) {
    throw new Error('quota response was not in the expected shape');
  }
  return {
    tenantId: asString(raw.tenantId),
    maxEnvironments: asNumber(raw.maxEnvironments),
    maxCpuMillicores: asNumber(raw.maxCpuMillicores),
    maxMemoryMb: asNumber(raw.maxMemoryMb),
    maxStorageGb: asNumber(raw.maxStorageGb),
    maxTotalCpuMillicores: asNumber(raw.maxTotalCpuMillicores),
    maxTotalMemoryMb: asNumber(raw.maxTotalMemoryMb),
    maxTotalStorageGb: asNumber(raw.maxTotalStorageGb),
  };
}

// SetTenantQuotaInput mirrors PUT .../quota's body: a PUT always fully
// replaces the row, so every cap is required on every call (see
// erun-backend-api/internal/routes/tenant_quotas.go's
// setTenantQuotaRequest/validateSetTenantQuotaRequest).
export type SetTenantQuotaInput = Omit<TenantQuota, 'tenantId'>;

export const quotaApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    getQuota: builder.query<TenantQuota, string>({
      query: (token) => ({ url: '/v1/quota', token, label: 'get quota' }),
      transformResponse: parseTenantQuota,
      providesTags: ['Quota'],
    }),

    setTenantQuota: builder.mutation<
      TenantQuota,
      { token: string; tenantId: string; input: SetTenantQuotaInput }
    >({
      query: ({ token, tenantId, input }) => ({
        url: `/v1/tenants/${encodeURIComponent(tenantId)}/quota`,
        method: 'PUT',
        body: input,
        token,
        label: 'set tenant quota',
      }),
      transformResponse: parseTenantQuota,
    }),
  }),
});

export const { useGetQuotaQuery, useSetTenantQuotaMutation } = quotaApi;
