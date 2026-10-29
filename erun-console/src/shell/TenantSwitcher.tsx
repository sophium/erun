import { SelectField } from 'erun-kit';
import type * as React from 'react';

import type { PlatformTenant, TenantReachability } from '../app/api/tenantsApi';
import {
  TENANT_REACHABILITY_ISSUER_NOT_MAPPED,
  TENANT_REACHABILITY_NO_ORG_MAPPING,
  TENANT_REACHABILITY_ORG_MISMATCH,
  TENANT_REACHABILITY_RESOLVABLE,
  useGetReachableTenantsQuery,
} from '../app/api/tenantsApi';
import type { OidcConfig } from '../auth/auth';
import { beginLogin } from '../auth/auth';
import { beginTenantSwitch } from './tenantSwitch';

export interface CurrentTenant {
  tenantId: string;
  name: string;
  type: string;
}

function unreachableReason(reachability: TenantReachability | undefined): string {
  switch (reachability) {
    case TENANT_REACHABILITY_ORG_MISMATCH:
      return 'belongs to a different organization than this account. Signing in again cannot reach it — it needs an account owned by that tenant’s organization.';
    case TENANT_REACHABILITY_NO_ORG_MAPPING:
      return 'has no organization on its identity-provider mapping, so no sign-in can resolve to it. An operator has to set one before anyone can reach it.';
    case TENANT_REACHABILITY_ISSUER_NOT_MAPPED:
      return 'is not mapped to the identity provider this account signs in with.';
    default:
      return 'cannot be reached with this account, and this console does not recognize the reason the platform reported.';
  }
}

// Unreachable memberships are shown, not filtered, so the broken mapping is
// visible to whoever must repair it.
function UnreachableMemberships({
  tenants,
}: {
  tenants: PlatformTenant[];
}): React.ReactElement | null {
  if (tenants.length === 0) {
    return null;
  }
  return (
    <div className="px-1 pt-2 text-xs text-sidebar-foreground/70">
      <p className="font-medium">Not reachable with this account</p>
      <ul className="mt-1 grid gap-1">
        {tenants.map((tenant) => (
          <li key={tenant.tenantId}>
            <span className="font-medium">{tenant.name}</span>{' '}
            {unreachableReason(tenant.reachability)}
          </li>
        ))}
      </ul>
    </div>
  );
}

function CurrentTenantLabel({ current }: { current: CurrentTenant }): React.ReactElement {
  return (
    <div className="px-1">
      <p className="truncate text-sm font-semibold text-sidebar-foreground">{current.name}</p>
      <p className="truncate text-xs text-sidebar-foreground/60">Tenant · {current.type}</p>
    </div>
  );
}

// TenantSwitcher renders a plain label (also while loading) unless more than
// one tenant is reachable. Switching starts a fresh OIDC sign-in because
// tenant resolution is a pure function of the token. Only RESOLVABLE
// memberships are offered; one with no verdict (older platform) stays offered.
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
  const memberships = (reachable ?? []).filter((tenant) => tenant.tenantId !== current.tenantId);
  const resolvable = memberships.filter(
    (tenant) =>
      tenant.reachability === undefined || tenant.reachability === TENANT_REACHABILITY_RESOLVABLE,
  );
  const unreachable = memberships.filter(
    (tenant) =>
      tenant.reachability !== undefined && tenant.reachability !== TENANT_REACHABILITY_RESOLVABLE,
  );

  if (oidc === undefined || resolvable.length === 0) {
    return (
      <div>
        <CurrentTenantLabel current={current} />
        <UnreachableMemberships tenants={unreachable} />
      </div>
    );
  }

  const options = [
    { value: current.tenantId, label: current.name },
    ...resolvable.map((tenant) => ({ value: tenant.tenantId, label: tenant.name })),
  ];

  return (
    <div>
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
      <UnreachableMemberships tenants={unreachable} />
    </div>
  );
}
