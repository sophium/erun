import type { TenantQuota } from '../app/api/quotaApi';
import { useGetQuotaQuery } from '../app/api/quotaApi';
import { queryErrorMessage } from '../app/queryError';

export type QuotaState =
  | { status: 'loading' }
  | { status: 'ready'; quota: TenantQuota }
  | { status: 'error'; message: string };

// useQuotaController reads the caller's own tenant's quota by default --
// self-service, unlike the operations-only PUT that sets another tenant's
// caps (see erun-backend-api/internal/routes/tenant_quotas.go's
// TenantQuotaReader comment: "a quota nobody can see is a support ticket") --
// or, with tenantId, a named target tenant's quota (operations-only,
// enforced server-side; see TenantQuotaDialog's prefill).
export function useQuotaController(token: string, tenantId?: string): QuotaState {
  const query = useGetQuotaQuery({ token, tenantId });
  if (query.error !== undefined) {
    return { status: 'error', message: queryErrorMessage(query.error) };
  }
  if (query.data !== undefined) {
    return { status: 'ready', quota: query.data };
  }
  return { status: 'loading' };
}
