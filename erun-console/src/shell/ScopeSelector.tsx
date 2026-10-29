import type * as React from 'react';

import type { PlatformTenant } from '../app/api/tenantsApi';
import type { ConsoleSectionId } from './sections';
import { sectionHonorsScope } from './sections';
import { TenantTargetSelect } from './TenantTargetSelect';

export interface ScopeTenant {
  tenantId: string;
  name: string;
}

// ScopeSelector picks which tenant's rows an OPERATIONS caller views without
// re-authenticating; `value` undefined means the caller's own tenant. It only
// claims scope applies on sections that honour it (sectionHonorsScope).
export function ScopeSelector({
  tenantType,
  tenants,
  ownTenant,
  value,
  active,
  onChange,
}: {
  tenantType: string;
  tenants: PlatformTenant[];
  ownTenant: ScopeTenant;
  value: string | undefined;
  active: ConsoleSectionId;
  onChange: (tenantId: string | undefined) => void;
}): React.ReactElement | null {
  if (tenantType !== 'OPERATIONS') {
    return null;
  }
  const scoped = value !== undefined && value !== ownTenant.tenantId;
  const honored = sectionHonorsScope(active);
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
      {scoped && honored && (
        <p className="px-1 pt-1 text-xs text-sidebar-foreground/70" role="status">
          Viewing another tenant's rows. Your own identity is still {ownTenant.name}.
        </p>
      )}
      {scoped && !honored && (
        <p className="px-1 pt-1 text-xs text-sidebar-foreground/70" role="status">
          This section doesn't use Administering — it always shows {ownTenant.name}'s own rows.
        </p>
      )}
    </div>
  );
}
