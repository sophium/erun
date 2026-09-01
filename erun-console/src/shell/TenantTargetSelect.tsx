import { SelectField } from 'erun-kit';
import type * as React from 'react';

import type { PlatformTenant } from '../app/api/tenantsApi';

// TenantTargetSelect is the shared "pick a target tenant" control: an
// OPERATIONS-only dropdown over every platform tenant, gated the same way
// `shell/sections.ts` gates the Users/Tenants nav entries. Every caller
// (identity/EnrollUserForm.tsx, identity/UsersPanel.tsx's EnrollForm, and
// shell/ScopeSelector.tsx) picks a target tenant for a different reason --
// where a write lands vs. which tenant's rows to read -- but the control and
// its gate are identical, so this is the one implementation. `id` is
// caller-supplied rather than baked in here because more than one of these
// can render on the same page at once (UsersPanel mounts both identity forms
// together), and two same-id selects would break label association.
export function TenantTargetSelect({
  id,
  label = 'Tenant',
  tenantType,
  tenants,
  value,
  onChange,
}: {
  id: string;
  label?: string;
  tenantType: string;
  tenants: PlatformTenant[];
  value: string;
  onChange: (tenantId: string) => void;
}): React.ReactElement | null {
  if (tenantType !== 'OPERATIONS') {
    return null;
  }
  return (
    <SelectField
      id={id}
      label={label}
      value={value}
      options={tenants.map((tenant) => ({ value: tenant.tenantId, label: tenant.name }))}
      onChange={onChange}
    />
  );
}
