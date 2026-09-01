import type * as React from 'react';

import type { PlatformTenant } from '../app/api/tenantsApi';
import { TenantTargetSelect } from './TenantTargetSelect';

export interface ScopeTenant {
  tenantId: string;
  name: string;
}

// ScopeSelector lets an OPERATIONS caller choose which tenant's rows the
// console currently administers, without re-authenticating -- distinct from
// TenantSwitcher (shell/TenantSwitcher.tsx), which trades the held credential
// for a different one via a fresh OIDC sign-in. Selecting a target here only
// changes the `tenantId` list queries send (e.g.
// environmentsApi.useListEnvironmentsQuery); the caller's own identity, and
// every permission check the API makes with it, never change (erun#1816).
// `value` is undefined for "my own tenant" (the default, and every caller's
// ordinary behavior before this existed); a defined value is a tenant other
// than the caller's own. `tenants` is fetched once by the caller
// (shell/AppShell.tsx) and shared with the Environments panel's tenant
// badges, rather than fetched again here.
export function ScopeSelector({
  tenantType,
  tenants,
  ownTenant,
  value,
  onChange,
}: {
  tenantType: string;
  tenants: PlatformTenant[];
  ownTenant: ScopeTenant;
  value: string | undefined;
  onChange: (tenantId: string | undefined) => void;
}): React.ReactElement | null {
  if (tenantType !== 'OPERATIONS') {
    return null;
  }
  const scoped = value !== undefined && value !== ownTenant.tenantId;
  return (
    <div>
      <TenantTargetSelect
        id="scope-target-tenant"
        label="Administering"
        tenantType={tenantType}
        tenants={tenants}
        value={value ?? ownTenant.tenantId}
        onChange={(tenantId) => {
          onChange(tenantId === ownTenant.tenantId ? undefined : tenantId);
        }}
      />
      {scoped && (
        <p className="px-1 pt-1 text-xs text-sidebar-foreground/70" role="status">
          Viewing another tenant's rows. Your own identity is still {ownTenant.name}.
        </p>
      )}
    </div>
  );
}
