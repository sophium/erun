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
  busyLabel: string;
  isLocal: boolean;
  selection: UISelection;
}

export function deriveEnvironmentRow(
  tenantName: string,
  environmentName: string,
  selectedSelection: UISelection | null,
  tenants: AppState['tenants'],
  isOpening: boolean,
  runningCommand: string,
): EnvironmentRowDerived {
  const selected =
    selectedSelection?.tenant === tenantName && selectedSelection.environment === environmentName;
  // busy reflects the per-env opening lifecycle AND any running activity
  // command (deploy, init, sshd init, doctor, ...) queued against the
  // env. Either signal is independent of which env is currently
  // selected, so concurrent work on multiple envs surfaces a spinner on
  // every row that's actually doing something — not just the one in the
  // active terminal.
  const busy = isOpening || runningCommand !== '';
  const busyLabel = environmentRowBusyLabel(tenantName, environmentName, isOpening, runningCommand);
  const environment = tenants
    .find((tenant) => tenant.name === tenantName)
    ?.environments.find((env) => env.name === environmentName);
  const isLocal = environment?.remote === false;
  return {
    selected,
    busy,
    busyLabel,
    isLocal,
    selection: { tenant: tenantName, environment: environmentName },
  };
}

function environmentRowBusyLabel(
  tenantName: string,
  environmentName: string,
  isOpening: boolean,
  runningCommand: string,
): string {
  const target = `${tenantName} / ${environmentName}`;
  if (runningCommand !== '') {
    const verb = activityCommandLabel(runningCommand);
    return `${verb} ${target}`;
  }
  if (isOpening) {
    return `Opening ${target}`;
  }
  return '';
}

function activityCommandLabel(command: string): string {
  switch (command) {
    case 'deploy':
      return 'Deploying';
    case 'init':
      return 'Initializing';
    case 'sshd-init':
      return 'Configuring SSH for';
    case 'doctor':
      return 'Running doctor on';
    case 'build':
      return 'Building';
    case 'push':
      return 'Pushing';
    case 'release':
      return 'Releasing';
    default:
      return 'Working on';
  }
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
