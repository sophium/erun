import { SelectField } from 'erun-kit';
import type * as React from 'react';

import type { PlatformTenant } from '../app/api/tenantsApi';

// TenantTargetSelect is the OPERATIONS-only target-tenant dropdown. `id` is
// caller-supplied because several can render on one page.
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
