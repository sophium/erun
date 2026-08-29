import { SelectField } from 'erun-kit';
import type * as React from 'react';

import { useGetReachableTenantsQuery } from '../app/api/tenantsApi';
import type { OidcConfig } from '../auth/auth';
import { beginLogin } from '../auth/auth';
import { beginTenantSwitch } from './tenantSwitch';

export interface CurrentTenant {
  tenantId: string;
  name: string;
  type: string;
}

// TenantSwitcher is the shell chrome above the sidebar nav: it names
// the scope everything below it operates on, and is always visible — never
// only while some menu is open. A caller whose identity maps to only one
// tenant sees it as a plain label; rendering a control here would suggest a
// choice that does not exist. This is also the safe default while
// getReachableTenants is still loading or has failed — a caller never briefly
// sees a control that then downgrades to a label once the real count is
// known, only the reverse.
//
// Tenant resolution is a pure function of the token (issuer + org claim), so
// picking another tenant cannot re-scope the one already held — it starts a
// fresh OIDC sign-in instead (see auth/auth.ts's `prompt` param and
// shell/tenantSwitch.ts, which records the intended target so the console can
// tell afterwards whether the new credential actually resolved to it).
export function TenantSwitcher({
  token,
  current,
  oidc,
}: {
  token: string;
  current: CurrentTenant;
  oidc: OidcConfig | undefined;
}): React.ReactElement {
  const { data: reachable } = useGetReachableTenantsQuery(token);
  const others = (reachable ?? []).filter((tenant) => tenant.tenantId !== current.tenantId);

  if (oidc === undefined || others.length === 0) {
    return (
      <div className="px-1">
        <p className="truncate text-sm font-semibold text-sidebar-foreground">{current.name}</p>
        <p className="truncate text-xs text-sidebar-foreground/60">Tenant · {current.type}</p>
      </div>
    );
  }

  const options = [
    { value: current.tenantId, label: current.name },
    ...others.map((tenant) => ({ value: tenant.tenantId, label: tenant.name })),
  ];

  return (
    <SelectField
      id="tenant-switcher"
      label="Tenant"
      value={current.tenantId}
      options={options}
      onChange={(tenantId) => {
        const target = options.find((option) => option.value === tenantId);
        if (target === undefined || target.value === current.tenantId) {
          return;
        }
        beginTenantSwitch({ tenantId: target.value, name: target.label });
        void beginLogin(oidc, 'select_account');
      }}
    />
  );
}
