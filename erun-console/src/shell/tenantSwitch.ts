// Switching tenants means a fresh OIDC sign-in; this persists the intended
// target across the redirect so the result can be checked.

const SWITCH_TARGET_KEY = 'erun.console.tenantSwitchTarget';

export interface TenantSwitchTarget {
  tenantId: string;
  name: string;
}

export function beginTenantSwitch(target: TenantSwitchTarget): void {
  sessionStorage.setItem(SWITCH_TARGET_KEY, JSON.stringify(target));
}

function isTenantSwitchTarget(value: unknown): value is TenantSwitchTarget {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as TenantSwitchTarget).tenantId === 'string' &&
    typeof (value as TenantSwitchTarget).name === 'string'
  );
}

// consumeTenantSwitchIntent is one-shot so a stale target never applies to a
// later unrelated sign-in.
export function consumeTenantSwitchIntent(): TenantSwitchTarget | undefined {
  const raw = sessionStorage.getItem(SWITCH_TARGET_KEY);
  sessionStorage.removeItem(SWITCH_TARGET_KEY);
  if (raw === null) {
    return undefined;
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    return isTenantSwitchTarget(parsed) ? parsed : undefined;
  } catch {
    return undefined;
  }
}
