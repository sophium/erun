import { CardAction, StatusBadge } from 'erun-kit';
import type * as React from 'react';

import type { PlatformTenant } from '../app/api/tenantsApi';
import type { OrgTarget } from './enrollOrgTargetController';

function tenantLabel(tenants: PlatformTenant[], tenantId: string): string {
  return tenants.find((tenant) => tenant.tenantId === tenantId)?.name ?? tenantId;
}

// Non-ready lookup states render plainly rather than falling back to the caller's own org.
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

// Renders nothing for the caller's own tenant.
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
