// RTK Query endpoint for reconciling the caller's own tenant name with this
// platform's declared identity (ERUN_TENANT) — the migration path for a
// platform bootstrapped before its own tenant name was read from it, whose
// OPERATIONS tenant is stuck under the placeholder name empty-database
// bootstrap fell back to.
import { isRecord, parseTenant, type Tenant } from 'erun-kit';

import { platformApi } from './platformApi';

export interface ReconcileTenantNameResult {
  tenant: Tenant;
  renamed: boolean;
}

function parseReconcileTenantNameResult(raw: unknown): ReconcileTenantNameResult {
  if (!isRecord(raw) || !isRecord(raw.tenant)) {
    throw new Error('reconcile tenant name response was not in the expected shape');
  }
  return {
    tenant: parseTenant(raw.tenant),
    renamed: raw.renamed === true,
  };
}

export const tenantsApi = platformApi.injectEndpoints({
  endpoints: (builder) => ({
    // Idempotent on the backend: a no-op when the name already matches or
    // this platform declares no identity, so the caller does not need to
    // check platformDeclaredName before calling.
    reconcileTenantName: builder.mutation<ReconcileTenantNameResult, string>({
      query: (token) => ({
        url: '/v1/tenants/reconcile-name',
        method: 'POST',
        token,
        label: 'reconcile tenant name',
      }),
      transformResponse: parseReconcileTenantNameResult,
      invalidatesTags: ['Config'],
    }),
  }),
});

export const { useReconcileTenantNameMutation } = tenantsApi;
