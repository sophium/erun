import { CardAction, StatusBadge } from 'erun-kit';
import type * as React from 'react';

import type { PlatformTenant } from '../app/api/tenantsApi';
import type { OrgTarget } from './enrollOrgTargetController';

function tenantLabel(tenants: PlatformTenant[], tenantId: string): string {
  return tenants.find((tenant) => tenant.tenantId === tenantId)?.name ?? tenantId;
}

// ScopedUsersStatus renders the org-target lookup's non-ready states for a
// scoped read: loading/error/unmapped are real states to state plainly
// rather than silently falling back to the caller's own org, which
// would render a confidently wrong page -- see erun-backend-api's
// identity.go listUsers doc for why guessing an org is refused outright.
// 'default'/'resolved' render nothing; the table takes over in either case.
export function ScopedUsersStatus({
  target,
  tenants,
  tenantId,
}: {
  target: OrgTarget;
  tenants: PlatformTenant[];
  tenantId: string;
}): React.ReactElement | null {
  const name = tenantLabel(tenants, tenantId);
  if (target.status === 'loading') {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Resolving {name}’s organization…
      </p>
    );
  }
  if (target.status === 'error') {
    return (
      <p className="text-sm text-destructive" role="alert">
        Could not resolve {name}’s organization: {target.message}
      </p>
    );
  }
  if (target.status === 'unmapped') {
    return (
      <p className="text-sm text-destructive" role="alert">
        {name} has no organization mapping yet, so its users cannot be listed here.
      </p>
    );
  }
  return null;
}

// UsersScopeBadge names which tenant the panel is viewing, the same
// "unlabeled scoped read is indistinguishable from the caller's own"
// treatment quota/QuotaPanel.tsx already gives (erun#1816) -- undefined
// scopeTenantId (the caller's own tenant) renders nothing.
export function UsersScopeBadge({
  scopeTenantId,
  tenants,
}: {
  scopeTenantId: string | undefined;
  tenants: PlatformTenant[];
}): React.ReactElement | null {
  if (scopeTenantId === undefined) {
    return null;
  }
  return (
    <CardAction>
      <StatusBadge tone="muted" label={tenantLabel(tenants, scopeTenantId)} showIcon={false} />
    </CardAction>
  );
}
