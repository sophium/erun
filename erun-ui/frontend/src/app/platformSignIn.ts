import type { UITenantDashboard } from '@/types';

// TENANT_SIGN_IN_AGAIN_MESSAGE is the exact operator-facing sentence
// tenant_platform_error.go's operatorPlatformError returns when a review
// write (comment, close, advance queue) discovers the signed-in platform
// token is no longer valid. Kept as an explicit contract here, rather than
// inferred from error shape, because a failed Wails call surfaces as a bare
// string with no room for a machine-readable reason to ride along (#1390).
//
// The tenant dashboard's own whole-dashboard load no longer needs this
// string-matching contract (#1393): it renders directly off
// UITenantDashboard.platformState, a typed enum distinguishing not-signed-in
// from not-enrolled from no-permission, none of which collapse to one
// sentence anymore. This module now covers only the write-action surfaces
// that still return a plain message string.
export const TENANT_SIGN_IN_AGAIN_MESSAGE =
  'Your sign-in is no longer valid for this tenant. Sign in again and retry.';

export function tenantNeedsSignIn(message: string): boolean {
  return message === TENANT_SIGN_IN_AGAIN_MESSAGE;
}

// resolveTenantPlatformAlias finds the erun-type cloud alias
// loginPrimaryCloudProvider needs to actually perform the sign-in a write
// failure like TENANT_SIGN_IN_AGAIN_MESSAGE names — the same alias the
// tenant dashboard already resolved server-side for this same tenant
// session, never a tenant's primary cloud alias (which may be any provider
// type and was the root cause of #1393).
export function resolveTenantPlatformAlias(
  dashboard: UITenantDashboard | null | undefined,
): string {
  return dashboard?.platformAlias?.trim() ?? '';
}
