// RTK Query endpoint for GET /v1/tenant-issuers (erun-backend-api/internal/
// routes/tenant_issuers.go): the caller's own tenant's issuer mappings by
// default, or another tenant's when the caller is operations-scoped and
// passes tenantId. The console's enroll-into-another-org flow (identityApi's
// EnrollForm) reads this to resolve a target tenant's org value before
// creating an identity provider account in it — orgFieldValue is the
// org id Zitadel expects.
import { asOptionalString, asString, isRecord } from 'erun-kit';

import { platformApi } from './platformApi';

export interface TenantIssuer {
  tenantId: string;
  issuer: string;
  name: string;
  orgFieldKey?: string;
  orgFieldValue?: string;
}

function parseTenantIssuer(raw: Record<string, unknown>): TenantIssuer {
  return {
    tenantId: asString(raw.tenantId),
    issuer: asString(raw.issuer),
    name: asString(raw.name),
    orgFieldKey: asOptionalString(raw.orgFieldKey),
    orgFieldValue: asOptionalString(raw.orgFieldValue),
  };
}

function parseTenantIssuerList(value: unknown): TenantIssuer[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(isRecord).map(parseTenantIssuer);
}

export const tenantIssuersApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    listTenantIssuers: builder.query<TenantIssuer[], { token: string; tenantId?: string }>({
      query: ({ token, tenantId }) => ({
        url:
          tenantId !== undefined && tenantId.length > 0
            ? `/v1/tenant-issuers?tenantId=${encodeURIComponent(tenantId)}`
            : '/v1/tenant-issuers',
        token,
        label: 'list tenant issuers',
      }),
      transformResponse: parseTenantIssuerList,
      providesTags: ['TenantIssuers'],
    }),
  }),
});

export const { useListTenantIssuersQuery } = tenantIssuersApi;
