import * as React from 'react';

export interface TenantTargetSelection {
  targetTenantId: string;
  setTargetTenantId: (tenantId: string) => void;
}

// useTenantTargetSelection is the state half of "pick a target tenant": a
// form starts pointed at the caller's own tenant and lets an OPERATIONS
// caller redirect it elsewhere. Previously a copy-pasted
// `useState(ownTenantId)` in both identity/EnrollUserForm.tsx and
// identity/UsersPanel.tsx (erun#1816).
export function useTenantTargetSelection(ownTenantId: string): TenantTargetSelection {
  const [targetTenantId, setTargetTenantId] = React.useState(ownTenantId);
  return { targetTenantId, setTargetTenantId };
}
