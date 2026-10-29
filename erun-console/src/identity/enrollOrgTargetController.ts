import * as React from 'react';

import { useListTenantIssuersQuery } from '../app/api/tenantIssuersApi';
import type { PlatformTenant } from '../app/api/tenantsApi';
import { useListTenantsQuery } from '../app/api/tenantsApi';
import { queryErrorMessage } from '../app/queryError';

// 'unmapped' (no org mapping) and 'error' (retryable lookup failure) stay distinct so
// it is always clear which org an identity lands in.
export type OrgTarget =
  | { status: 'default' }
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'unmapped' }
  | { status: 'resolved'; orgId: string };

export function resolveOrgTarget(
  isOwnTenant: boolean,
  isLoading: boolean,
  error: unknown,
  orgFieldValues: (string | undefined)[],
): OrgTarget {
  if (isOwnTenant) {
    return { status: 'default' };
  }
  if (isLoading) {
    return { status: 'loading' };
  }
  if (error !== undefined) {
    return { status: 'error', message: queryErrorMessage(error) };
  }
  const orgId = orgFieldValues.find((value) => value !== undefined && value !== '');
  return orgId === undefined ? { status: 'unmapped' } : { status: 'resolved', orgId };
}

// useEnrollOrgTarget resolves a target tenant to its Zitadel org id; skipped for the caller's own tenant.
export function useEnrollOrgTarget(
  token: string,
  ownTenantId: string,
  targetTenantId: string,
): { tenants: PlatformTenant[]; orgTarget: OrgTarget } {
  const tenantsQuery = useListTenantsQuery(token);
  const tenants = React.useMemo(() => tenantsQuery.data ?? [], [tenantsQuery.data]);
  const isOwnTenant = targetTenantId === ownTenantId;
  const issuersQuery = useListTenantIssuersQuery(
    { token, tenantId: targetTenantId },
    { skip: isOwnTenant },
  );
  const orgTarget = resolveOrgTarget(
    isOwnTenant,
    issuersQuery.isLoading || issuersQuery.isFetching,
    issuersQuery.error,
    (issuersQuery.data ?? []).map((issuer) => issuer.orgFieldValue),
  );
  return { tenants, orgTarget };
}
