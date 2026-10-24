// RTK Query endpoints for tenant quotas: a self-service read and an operations-only write.
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

// PUT fully replaces the row, so every cap is required.
export type SetTenantQuotaInput = Omit<TenantQuota, 'tenantId'>;

export const quotaApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // `tenantId` is operations-only and enforced server-side.
    getQuota: builder.query<TenantQuota, { token: string; tenantId?: string }>({
      query: ({ token, tenantId }) => ({
        url:
          tenantId === undefined
            ? '/v1/quota'
            : `/v1/quota?tenantId=${encodeURIComponent(tenantId)}`,
        token,
        label: 'get quota',
      }),
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
      invalidatesTags: ['Quota'],
    }),
  }),
});

export const { useGetQuotaQuery, useSetTenantQuotaMutation } = quotaApi;
