import type { UITenantDashboard } from '@/types';

// TENANT_SIGN_IN_AGAIN_MESSAGE must match tenant_platform_error.go exactly: a failed
// Wails call is a bare string, so the sentence is the contract.
export const TENANT_SIGN_IN_AGAIN_MESSAGE =
  'Your sign-in is no longer valid for this tenant. Sign in again and retry.';

export function tenantNeedsSignIn(message: string): boolean {
  return message === TENANT_SIGN_IN_AGAIN_MESSAGE;
}

// resolveTenantPlatformAlias returns the erun-type alias, never the tenant's
// primary alias, which may be any provider.
export function resolveTenantPlatformAlias(
  dashboard: UITenantDashboard | null | undefined,
): string {
  return dashboard?.platformAlias?.trim() ?? '';
}
