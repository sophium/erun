import {
  environmentTypeIsHost,
  environmentTypeIsRemoteWorktree,
  environmentTypeIsRuntime,
} from '@/app/environmentType';
import type { AppState } from '@/app/state';
import type { StatusDotState } from '@/components/app/Sidebar.StatusDot';
import { CloudProviderERun, type UIEnvironment, type UISelection } from '@/types';
import type {
  UIErunVersion,
  UIRuntimeImageLineMismatch,
  UIRuntimeVersionLine,
} from '@/uiRuntimeVersionLineTypes';

// pendingForTenant returns the optimistic selection for an env still being created.
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

// environmentIsLocal reports whether the env runs against a worktree mounted from this machine.
export function environmentIsLocal(environment: UIEnvironment | undefined): boolean {
  return !environmentTypeIsRemoteWorktree(environment?.type);
}

// environmentIsHost reports whether the env is a host environment (no pod, no cluster).
export function environmentIsHost(environment: UIEnvironment | undefined): boolean {
  return environmentTypeIsHost(environment?.type);
}

// environmentUsesDindSidecar reports whether the env's pod carries the erun-dind
// sidecar; mirrors $dindEnabled in erun-devops/k8s/erun-devops/templates/service.yaml.
export function environmentUsesDindSidecar(environment: UIEnvironment | undefined): boolean {
  return !environmentTypeIsHost(environment?.type) && !environmentTypeIsRuntime(environment?.type);
}

export interface EnvironmentRowDerived {
  selected: boolean;
  busy: boolean;
  busyLabel: string;
  // The label describes the environment's own condition, not a desktop operation,
  // so surfaces already naming the environment can skip it.
  busyFromEnvironment: boolean;
  isLocal: boolean;
  isHost: boolean;
  usageExcludesBuilds: boolean;
  runtimeVersion: string;
  runtimeVersionLine: UIRuntimeVersionLine | undefined;
  erunVersion: UIErunVersion | undefined;
  runtimeImageLineMismatch: UIRuntimeImageLineMismatch | undefined;
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
  envBusy: boolean,
  envBusyDetail: string,
  envObserved: boolean,
): EnvironmentRowDerived {
  const selected =
    selectedSelection?.tenant === tenantName && selectedSelection.environment === environmentName;
  // envBusy is the only input true regardless of who started the work; the
  // others are desktop-local.
  const busy = environmentRowIsBusy(
    isOpening,
    runningCommand,
    aiBusy,
    reconnecting,
    envBusy,
    envObserved,
  );
  const busyFromEnvironment =
    envBusy &&
    environmentRowCommandLabel(
      tenantName,
      environmentName,
      isOpening,
      runningCommand,
      reconnecting,
    ) === '';
  const busyLabel = environmentRowBusyLabel(
    tenantName,
    environmentName,
    isOpening,
    runningCommand,
    aiBusy,
    reconnecting,
    envBusy,
    envBusyDetail,
  );
  const environment = tenants
    .find((tenant) => tenant.name === tenantName)
    ?.environments.find((env) => env.name === environmentName);
  const isLocal = environmentIsLocal(environment);
  const isHost = environmentIsHost(environment);
  const usageExcludesBuilds = environmentUsesDindSidecar(environment);
  return {
    selected,
    busy,
    busyLabel,
    busyFromEnvironment,
    isLocal,
    isHost,
    usageExcludesBuilds,
    ...environmentVersionFields(environment),
    selection: { tenant: tenantName, environment: environmentName },
  };
}

function environmentVersionFields(environment: UIEnvironment | undefined): {
  runtimeVersion: string;
  runtimeVersionLine: UIRuntimeVersionLine | undefined;
  erunVersion: UIErunVersion | undefined;
  runtimeImageLineMismatch: UIRuntimeImageLineMismatch | undefined;
} {
  return {
    runtimeVersion: environment?.runtimeVersion?.trim() ?? '',
    runtimeVersionLine: environment?.runtimeVersionLine,
    erunVersion: environment?.erunVersion,
    runtimeImageLineMismatch: environment?.runtimeImageLineMismatch,
  };
}

// The environment's own verdict can clear desktop-local latches that would
// otherwise stick; a wedged edge gives no verdict, and isOpening/reconnecting
// stay authoritative since the environment cannot have seen them yet.
function environmentRowIsBusy(
  isOpening: boolean,
  runningCommand: string,
  aiBusy: boolean,
  reconnecting: boolean,
  envBusy: boolean,
  envObserved: boolean,
): boolean {
  if (envBusy || isOpening || reconnecting) {
    return true;
  }
  if (envObserved) {
    return false;
  }
  return runningCommand !== '' || aiBusy;
}

function environmentRowBusyLabel(
  tenantName: string,
  environmentName: string,
  isOpening: boolean,
  runningCommand: string,
  aiBusy: boolean,
  reconnecting: boolean,
  envBusy: boolean,
  envBusyDetail: string,
): string {
  const target = `${tenantName} / ${environmentName}`;
  const command = environmentRowCommandLabel(
    tenantName,
    environmentName,
    isOpening,
    runningCommand,
    reconnecting,
  );
  if (command !== '') {
    return command;
  }
  if (envBusy) {
    return envBusyDetail !== '' ? `${target} is busy — ${envBusyDetail}` : `${target} is busy`;
  }
  if (aiBusy) {
    return `AI tab working on ${target}`;
  }
  return '';
}

// environmentCardActivityLabel blanks the label only when the indicator will read
// 'busy': a sticky stopped/failed condition outranks busy and can lag a fresher
// busy observation.
export function environmentCardActivityLabel(
  busy: boolean,
  busyFromEnvironment: boolean,
  busyLabel: string,
  dot: StatusDotState,
): string {
  if (!busy) {
    return '';
  }
  if (busyFromEnvironment && dot === 'busy') {
    return '';
  }
  return busyLabel;
}

// Order decides which in-flight operation names the row.
function environmentRowCommandLabel(
  tenantName: string,
  environmentName: string,
  isOpening: boolean,
  runningCommand: string,
  reconnecting: boolean,
): string {
  const target = `${tenantName} / ${environmentName}`;
  if (runningCommand !== '') {
    return `${activityCommandLabel(runningCommand)} ${target}`;
  }
  if (isOpening) {
    return `Opening ${target}`;
  }
  if (reconnecting) {
    return `Reconnecting ${target}`;
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
// type, plus an erun-hosted alias no tenant attaches.
export function sidebarCloudAliases(input: CloudAliasRowInputs): string[] {
  const tenantName = input.dashboardTenant || (input.selected?.tenant ?? '');
  const tenant = input.tenants.find((candidate) => candidate.name === tenantName);
  const aliasByType = tenant
    ? firstAliasPerProviderType(tenantCloudAliases(tenant), input.cloudProviders)
    : new Map<string, string>();
  const unattached = firstUnattachedERunAlias(input.tenants, input.cloudProviders);
  if (unattached && !aliasByType.has(CloudProviderERun)) {
    aliasByType.set(CloudProviderERun, unattached);
  }
  return orderCloudAliasRows(aliasByType);
}

function firstUnattachedERunAlias(
  tenants: AppState['tenants'],
  cloudProviders: AppState['cloudProviders'],
): string | undefined {
  const attached = new Set(tenants.flatMap((tenant) => tenantCloudAliases(tenant)));
  return cloudProviders.find(
    (provider) =>
      provider.provider.trim().toLowerCase() === CloudProviderERun && !attached.has(provider.alias),
  )?.alias;
}

// The primary alias goes first: downstream dedup keeps the first alias per type.
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

// Two stopped values because the recovery differs: a stopped cloud context starts
// from the titlebar, a runtime scaled to zero is woken by `erun open`.
const ENV_STATE_STOPPED = 'stopped';
const ENV_STATE_RUNTIME_STOPPED = 'runtime-stopped';
const ENV_STATE_FAILED = 'failed';

// Stopped never renders the failure glyph; sticky conditions outrank busy; a
// forward outage reads as failed even on an unopened row.
function environmentStatusDot(envState: string, busy = false, outage = false): StatusDotState {
  if (envState === ENV_STATE_FAILED || outage) {
    return 'failed';
  }
  if (envState === ENV_STATE_STOPPED || envState === ENV_STATE_RUNTIME_STOPPED) {
    return 'stopped';
  }
  return busy ? 'busy' : 'running';
}

// EnvironmentIndicator is the single derived row state, so callers cannot disagree with half of it.
export interface EnvironmentIndicator {
  visible: boolean;
  dot: StatusDotState;
  // opened means the desktop owns tabs; a CLI-opened env is reachable but not opened.
  opened: boolean;
  condition: string;
  activity: string;
}

export interface EnvironmentIndicatorInputs {
  name: string;
  envState: string;
  isOpen: boolean;
  reachable: boolean;
  // outage is a lost port-forward, kept separate from reachable because the dead
  // forward's listener still looks reachable.
  outage: boolean;
  busy: boolean;
  detail: string;
}

export function environmentIndicator(raw: EnvironmentIndicatorInputs): EnvironmentIndicator {
  // The sticky condition describes a desktop session, so it is dropped once no
  // tabs remain, except a failed deploy, which persists until the env answers again.
  const keepFailedWhenClosed = raw.envState === ENV_STATE_FAILED && !raw.reachable;
  const input = raw.isOpen || keepFailedWhenClosed ? raw : { ...raw, envState: '' };
  const dot = environmentStatusDot(input.envState, input.busy, input.outage);
  const visible = input.isOpen || input.reachable || input.busy || input.outage || dot === 'failed';
  return {
    visible,
    dot,
    opened: input.isOpen,
    condition: environmentCondition(input, dot),
    activity: environmentActivityLabel(input, dot),
  };
}

function environmentActivityLabel(input: EnvironmentIndicatorInputs, dot: StatusDotState): string {
  if (input.outage) {
    return `Unreachable — ${FORWARD_OUTAGE_RECOVERY}`;
  }
  if (dot === 'failed') {
    return 'Deploy failed — recover from Activities';
  }
  if (dot === 'stopped') {
    return `Stopped — ${environmentStateRecovery(input.envState)}`;
  }
  if (dot === 'busy') {
    return input.detail ? `Busy — ${input.detail}` : 'Busy';
  }
  if (input.isOpen) {
    return 'Idle';
  }
  if (input.reachable) {
    return 'In use elsewhere — not opened here';
  }
  return 'Not open';
}

// Deploy, not re-open: the desktop already re-opened it without success.
const FORWARD_OUTAGE_RECOVERY = 'its connection is dead; deploy it to bring the runtime back';

function environmentCondition(input: EnvironmentIndicatorInputs, dot: StatusDotState): string {
  if (input.outage) {
    return `${input.name} is unreachable — ${FORWARD_OUTAGE_RECOVERY}`;
  }
  if (dot === 'failed') {
    return `${input.name} deploy failed — ${environmentStateRecovery(input.envState)}`;
  }
  if (dot === 'stopped') {
    return `${input.name} is stopped — ${environmentStateRecovery(input.envState)}`;
  }
  if (dot === 'busy') {
    return input.detail ? `${input.name} is busy — ${input.detail}` : `${input.name} is busy`;
  }
  if (input.isOpen) {
    return `${input.name} is running`;
  }
  if (input.reachable) {
    return `${input.name} is running and in use elsewhere — not opened here`;
  }
  return `${input.name} is not open`;
}

function environmentStateRecovery(envState: string): string {
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
