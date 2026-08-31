import * as React from 'react';

import { useListTenantIssuersQuery } from '../app/api/tenantIssuersApi';
import type { PlatformTenant } from '../app/api/tenantsApi';
import { useListTenantsQuery } from '../app/api/tenantsApi';
import { queryErrorMessage } from '../app/queryError';

// OrgTarget is EnrollForm's own distinction between "nothing chosen — creates
// in the platform's own org, today's behaviour", "chosen, but this tenant's
// org mapping could not be read" (a transient lookup failure, retryable), and
// "chosen, but this tenant genuinely has no org mapping yet" (a real state,
// not a failure) — collapsing any of these into another has previously
// hidden which org an identity actually landed in. 'loading' and 'resolved'
// round out the render states while the lookup is in flight or has found a
// usable mapping.
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

// useEnrollOrgTarget resolves a chosen target tenant to the Zitadel org it
// maps to (tenant_issuers.org_field_value, via GET /v1/tenant-issuers) so
// EnrollForm can pass that org id as orgId rather than asking the operator
// for a raw org id directly. Skips the lookup entirely when the target is the
// caller's own tenant — the existing, always-available default.
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
