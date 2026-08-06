import { environmentTypeIsRemoteWorktree } from '@/app/environmentType';
import type { AppState } from '@/app/state';
import type { StatusDotState } from '@/components/app/Sidebar.StatusDot';
import type { UIEnvironment, UISelection } from '@/types';

// pendingForTenant returns the optimistic selection for an env being created but
// not yet present in state, so the sidebar can render a placeholder row that
// keeps system status visible during the ~1–2 min init run.
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

// environmentIsLocal reports whether the env runs against a worktree mounted from
// this machine (a local-agent env).
export function environmentIsLocal(environment: UIEnvironment | undefined): boolean {
  return !environmentTypeIsRemoteWorktree(environment?.type);
}

export interface EnvironmentRowDerived {
  selected: boolean;
  busy: boolean;
  busyLabel: string;
  isLocal: boolean;
  runtimeVersion: string;
  selection: UISelection;
}

export function deriveEnvironmentRow(
  tenantName: string,
  environmentName: string,
  selectedSelection: UISelection | null,
  tenants: AppState['tenants'],
  isOpening: boolean,
  runningCommand: string,
  aiBusy: boolean,
  reconnecting: boolean,
): EnvironmentRowDerived {
  const selected =
    selectedSelection?.tenant === tenantName && selectedSelection.environment === environmentName;
  // busy is scoped to this env and independent of which env is selected, so
  // concurrent work on multiple envs shows a spinner on every row that's actually
  // doing something — not just the one in the active terminal.
  const busy = isOpening || runningCommand !== '' || aiBusy || reconnecting;
  const busyLabel = environmentRowBusyLabel(
    tenantName,
    environmentName,
    isOpening,
    runningCommand,
    aiBusy,
    reconnecting,
  );
  const environment = tenants
    .find((tenant) => tenant.name === tenantName)
    ?.environments.find((env) => env.name === environmentName);
  const isLocal = environmentIsLocal(environment);
  return {
    selected,
    busy,
    busyLabel,
    isLocal,
    runtimeVersion: environment?.runtimeVersion?.trim() ?? '',
    selection: { tenant: tenantName, environment: environmentName },
  };
}

function environmentRowBusyLabel(
  tenantName: string,
  environmentName: string,
  isOpening: boolean,
  runningCommand: string,
  aiBusy: boolean,
  reconnecting: boolean,
): string {
  const target = `${tenantName} / ${environmentName}`;
  if (runningCommand !== '') {
    const verb = activityCommandLabel(runningCommand);
    return `${verb} ${target}`;
  }
  if (isOpening) {
    return `Opening ${target}`;
  }
  if (reconnecting) {
    return `Reconnecting ${target}`;
  }
  if (aiBusy) {
    return `AI tab working on ${target}`;
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

export interface CloudAliasRowInputs {
  tenants: AppState['tenants'];
  cloudProviders: AppState['cloudProviders'];
  selected: AppState['selected'];
  dashboardTenant: string;
}

// sidebarCloudAliases returns the active tenant's cloud aliases, one per provider
// type, so an env wired to both an AWS account and a Cloudflare token shows two
// independent login rows.
export function sidebarCloudAliases(input: CloudAliasRowInputs): string[] {
  const tenantName = input.dashboardTenant || (input.selected?.tenant ?? '');
  const tenant = input.tenants.find((candidate) => candidate.name === tenantName);
  if (!tenant) {
    return [];
  }
  const aliasByType = firstAliasPerProviderType(tenantCloudAliases(tenant), input.cloudProviders);
  return orderCloudAliasRows(aliasByType);
}

// The primary alias goes first so its type wins when two aliases share a
// provider type — dedup downstream keeps only the first alias per type.
function tenantCloudAliases(tenant: AppState['tenants'][number]): string[] {
  const aliases = [...(tenant.cloudProviderAliases ?? [])];
  const primary = tenant.primaryCloudProviderAlias?.trim();
  if (primary && !aliases.includes(primary)) {
    aliases.unshift(primary);
  }
  return aliases;
}

function firstAliasPerProviderType(
  aliases: string[],
  cloudProviders: AppState['cloudProviders'],
): Map<string, string> {
  const providerTypeByAlias = new Map(
    cloudProviders.map((provider) => [provider.alias, provider.provider.trim().toLowerCase()]),
  );
  const aliasByType = new Map<string, string>();
  for (const rawAlias of aliases) {
    const alias = rawAlias.trim();
    if (alias === '') {
      continue;
    }
    const type = providerTypeByAlias.get(alias) ?? cloudProviderTypeFromAlias(alias);
    if (!aliasByType.has(type)) {
      aliasByType.set(type, alias);
    }
  }
  return aliasByType;
}

// Mirrors erun-common's alias-type fallback; keep the two in sync.
function cloudProviderTypeFromAlias(alias: string): string {
  const at = alias.lastIndexOf('@');
  if (at <= 0 || at === alias.length - 1) {
    return 'aws';
  }
  return (
    alias
      .slice(at + 1)
      .trim()
      .toLowerCase() || 'aws'
  );
}

const cloudAliasRowOrder = ['aws', 'cloudflare'];

function orderCloudAliasRows(aliasByType: Map<string, string>): string[] {
  const ordered: string[] = [];
  const remaining = new Map(aliasByType);
  for (const type of cloudAliasRowOrder) {
    const alias = remaining.get(type);
    if (alias) {
      ordered.push(alias);
      remaining.delete(type);
    }
  }
  for (const type of [...remaining.keys()].sort()) {
    const alias = remaining.get(type);
    if (alias) {
      ordered.push(alias);
    }
  }
  return ordered;
}

// The env-status values the Go side emits (erun-ui/ui_model.go). There are two
// stopped values because the recovery differs: a stopped cloud context is
// started from the titlebar, while a runtime scaled to zero is woken by opening
// the environment (which runs `erun open`, and that is what scales it back up).
export const ENV_STATE_STOPPED = 'stopped';
export const ENV_STATE_RUNTIME_STOPPED = 'runtime-stopped';
export const ENV_STATE_FAILED = 'failed';

// environmentStatusDot maps an env state onto the shared indicator shape. Both
// stopped kinds read as stopped — a stopped environment is not a failure, and
// must never render the failure glyph.
export function environmentStatusDot(envState: string): StatusDotState {
  if (envState === ENV_STATE_FAILED) {
    return 'failed';
  }
  if (envState === ENV_STATE_STOPPED || envState === ENV_STATE_RUNTIME_STOPPED) {
    return 'stopped';
  }
  return 'running';
}

// environmentStateRecovery names the action that gets the environment back, so
// the state is never shown without the way out of it. Empty for a running env.
export function environmentStateRecovery(envState: string): string {
  if (envState === ENV_STATE_STOPPED) {
    return 'start it from the titlebar';
  }
  if (envState === ENV_STATE_RUNTIME_STOPPED) {
    return 'click it in the sidebar to start it again';
  }
  if (envState === ENV_STATE_FAILED) {
    return 'recover from Activities or re-click the row';
  }
  return '';
}
