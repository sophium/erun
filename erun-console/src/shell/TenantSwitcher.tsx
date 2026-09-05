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

// unreachableReason turns the platform's verdict into the sentence a caller
// can act on: why re-signing-in cannot help, and who can change that. A
// verdict this console does not recognize says exactly that rather than
// inventing a cause nobody checked.
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

// UnreachableMemberships names the memberships that exist but can never be
// signed into. They are deliberately shown rather than filtered away: dropping
// them would trade one silence (a switch target that always fails) for another
// (a membership nobody can see is broken), and the operator who repairs the
// mapping is usually the person reading this.
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
//
// Membership is not the same key as resolution, so a membership the platform
// reports as anything other than RESOLVABLE is never offered. Offering those
// sent the caller through a full OIDC round trip to land back where they
// started, and the mismatch banner afterwards was good behaviour on an offer
// that should never have been made.
//
// A membership with no verdict at all is a platform too old to have one, not a
// broken tenant: it stays offered (with the mismatch banner as the safety net
// it has always been) rather than silently disappearing from the switcher on a
// version skew, and it is never named as unreachable either.
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
