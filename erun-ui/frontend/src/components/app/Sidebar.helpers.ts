import { environmentTypeIsRemoteWorktree } from '@/app/environmentType';
import type { AppState } from '@/app/state';
import type { UIEnvironment, UISelection } from '@/types';

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

// environmentIsLocal reports whether the env's worktree is mounted from this
// machine, i.e. a local-agent env. It keys off the resolved environment type
// (erun-common computes `type` via EnvConfig.ResolvedType() and lists it on
// every env): an env is local exactly when its worktree is not remote. This
// drives both the LOCAL badge and the `(local)` accessible-label suffix, which
// share the flag.
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
  // busy reflects the per-env opening lifecycle AND any running activity
  // command (deploy, init, sshd init, doctor, ...) queued against the
  // env, AND the debounced "AI tab is producing output" signal from
  // recordAIActivity in the Go terminal layer, AND the in-flight
  // reconnect (review-pane redeploy) scoped to this env. Every source
  // is independent of which env is currently selected, so concurrent
  // work on multiple envs surfaces a spinner on every row that's
  // actually doing something — not just the one in the active terminal.
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

// sidebarCloudAliases returns the cloud aliases the active tenant uses, one per
// provider type, in a stable order (AWS first, then Cloudflare, then any other
// type alphabetically). The active tenant is the dashboard tenant when one is
// open, otherwise the selected env's tenant. Each provider type that the tenant
// references through its cloudProviderAliases (or its primary alias)
// contributes exactly one row, so an env wired to both an AWS account and a
// Cloudflare token shows two independent login rows. The provider type is
// resolved from the configured cloudProviders; an alias with no matching
// provider config still surfaces with an empty provider so the operator can see
// and recover from it.
export function sidebarCloudAliases(input: CloudAliasRowInputs): string[] {
  const tenantName = input.dashboardTenant || (input.selected?.tenant ?? '');
  const tenant = input.tenants.find((candidate) => candidate.name === tenantName);
  if (!tenant) {
    return [];
  }
  const aliasByType = firstAliasPerProviderType(tenantCloudAliases(tenant), input.cloudProviders);
  return orderCloudAliasRows(aliasByType);
}

// tenantCloudAliases collects the aliases a tenant references: its configured
// list with the primary alias pulled to the front (so the primary's type wins
// when two aliases share a type).
function tenantCloudAliases(tenant: AppState['tenants'][number]): string[] {
  const aliases = [...(tenant.cloudProviderAliases ?? [])];
  const primary = tenant.primaryCloudProviderAlias?.trim();
  if (primary && !aliases.includes(primary)) {
    aliases.unshift(primary);
  }
  return aliases;
}

// firstAliasPerProviderType keeps the first alias seen for each provider type,
// so each type contributes exactly one row. The type is resolved from the
// configured cloudProviders, falling back to the alias's "@type" suffix.
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

// cloudProviderTypeFromAlias mirrors erun-common's fallback: the provider type
// is the suffix after the last "@", defaulting to AWS for legacy / unparseable
// aliases.
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
