import type { UITenant } from '@/types';

// The exact operator-facing sentences erun-ui's Go side returns for an
// identity the platform no longer accepts — tenant_dashboard.go's
// tenantDashboardIdentityError (whole-dashboard/whole-detail load) and
// tenant_platform_error.go's operatorPlatformError (a write that discovers
// the same thing). Kept as an explicit contract here, rather than inferred
// from error shape, because a failed Wails call surfaces as a bare string
// with no room for a machine-readable reason to ride along (#1390).
export const TENANT_IDENTITY_SIGN_IN_MESSAGE =
  "This tenant's platform did not accept the signed-in identity. Sign in to the tenant's cloud provider again.";
export const TENANT_SIGN_IN_AGAIN_MESSAGE =
  'Your sign-in is no longer valid for this tenant. Sign in again and retry.';

export function tenantNeedsSignIn(message: string): boolean {
  return message === TENANT_IDENTITY_SIGN_IN_MESSAGE || message === TENANT_SIGN_IN_AGAIN_MESSAGE;
}

// resolveTenantCloudAlias finds the cloud alias loginPrimaryCloudProvider
// needs to actually perform the sign-in a message like the ones above names.
export function resolveTenantCloudAlias(tenants: UITenant[], tenantName: string): string {
  return (
    tenants.find((candidate) => candidate.name === tenantName)?.primaryCloudProviderAlias?.trim() ??
    ''
  );
}
