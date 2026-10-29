import * as React from 'react';

export interface TenantTargetSelection {
  targetTenantId: string;
  setTargetTenantId: (tenantId: string) => void;
}

// useTenantTargetSelection defaults to the caller's own tenant.
export function useTenantTargetSelection(ownTenantId: string): TenantTargetSelection {
  const [targetTenantId, setTargetTenantId] = React.useState(ownTenantId);
  return { targetTenantId, setTargetTenantId };
}
