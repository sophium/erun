import type { TenantQuota } from '../app/api/quotaApi';
import { useGetQuotaQuery } from '../app/api/quotaApi';
import { queryErrorMessage } from '../app/queryError';

export type QuotaState =
  | { status: 'loading' }
  | { status: 'ready'; quota: TenantQuota }
  | { status: 'error'; message: string };

// tenantId is operations-only and enforced server-side.
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
