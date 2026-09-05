// Tenant resolution is a pure function of the token (issuer + org claim), so
// "switching" cannot be a client-side variable — it means acquiring a
// different credential via a fresh OIDC sign-in (see auth/auth.ts's `prompt`
// param). This module is the one piece of state that survives that redirect:
// which tenant the caller was trying to reach, so the console can tell,
// after the round trip, whether the credential that came back actually
// resolves to that tenant or merely re-authenticated the same one.

const SWITCH_TARGET_KEY = 'erun.console.tenantSwitchTarget';

export interface TenantSwitchTarget {
  tenantId: string;
  name: string;
}

// beginTenantSwitch records the intended target before the caller is
// redirected away for a fresh sign-in.
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

// consumeTenantSwitchIntent reads and clears a pending switch target. It is
// one-shot by design: a stale target left over from an abandoned attempt (or
// an ordinary sign-in that never touched the switcher) must not silently
// reappear against some unrelated later sign-in.
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
