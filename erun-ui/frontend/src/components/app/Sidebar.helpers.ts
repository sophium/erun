import type { AppState } from '@/app/state';
import type { UISelection } from '@/types';

// pendingForTenant returns the optimistic selection that is being
// created right now, when the matching env is not yet in
// state.tenants. The sidebar renders a placeholder row for it so
// Nielsen #1 (visibility of system status) holds during the
// ~1–2 min init runs. The placeholder disappears once
// reloadStateAfterEnvironmentChange picks up the new env, or when
// `environment-init-failed` reverts state.selected.
export function pendingForTenant(
  tenants: AppState['tenants'],
  selected: AppState['selected'],
  tenantName: string,
): UISelection | null {
  if (selected?.tenant !== tenantName) {
    return null;
  }
  const tenant = tenants.find((entry) => entry.name === selected.tenant);
  if (!tenant) {
    return null;
  }
  if (tenant.environments.some((env) => env.name === selected.environment)) {
    return null;
  }
  return selected;
}

export interface EnvironmentRowDerived {
  selected: boolean;
  busy: boolean;
  isLocal: boolean;
  selection: UISelection;
}

export function deriveEnvironmentRow(
  tenantName: string,
  environmentName: string,
  selectedSelection: UISelection | null,
  tenants: AppState['tenants'],
  terminalBusy: boolean,
): EnvironmentRowDerived {
  const selected =
    selectedSelection?.tenant === tenantName && selectedSelection.environment === environmentName;
  const busy = terminalBusy && selected;
  const environment = tenants
    .find((tenant) => tenant.name === tenantName)
    ?.environments.find((env) => env.name === environmentName);
  const isLocal = environment?.remote === false;
  return {
    selected,
    busy,
    isLocal,
    selection: { tenant: tenantName, environment: environmentName },
  };
}

export interface PrimaryCloudAliasInputs {
  tenants: AppState['tenants'];
  cloudProviders: AppState['cloudProviders'];
  selected: AppState['selected'];
  dashboardTenant: string;
  sidebarBusy: boolean;
  sidebarAction: AppState['sidebarCloudAliasAction'];
}

export function primaryCloudAliasFor(input: PrimaryCloudAliasInputs): string | undefined {
  const tenantName = input.dashboardTenant || (input.selected?.tenant ?? '');
  return input.tenants
    .find((candidate) => candidate.name === tenantName)
    ?.primaryCloudProviderAlias?.trim();
}
