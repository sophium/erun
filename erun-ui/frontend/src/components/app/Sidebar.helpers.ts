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
  envBusy: boolean,
  envBusyDetail: string,
): EnvironmentRowDerived {
  const selected =
    selectedSelection?.tenant === tenantName && selectedSelection.environment === environmentName;
  // busy is scoped to this env and independent of which env is selected, so
  // concurrent work on multiple envs shows a spinner on every row that's actually
  // doing something — not just the one in the active terminal.
  //
  // envBusy is what the environment says about itself, and it is the only input
  // here that is true regardless of who started the work. The other four are
  // desktop-local: they report what this desktop launched, so an environment
  // driven by `erun` from a terminal, by an orchestrator over MCP, or by a
  // detached job was doing real work behind a row that looked idle.
  const busy = isOpening || runningCommand !== '' || aiBusy || reconnecting || envBusy;
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
  envBusy: boolean,
  envBusyDetail: string,
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
  if (envBusy) {
    return envBusyDetail !== ''
      ? `${target} is busy — ${envBusyDetail}`
      : `${target} is busy`;
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
const ENV_STATE_STOPPED = 'stopped';
const ENV_STATE_RUNTIME_STOPPED = 'runtime-stopped';
const ENV_STATE_FAILED = 'failed';

// environmentStatusDot maps an env state onto the shared indicator shape. Both
// stopped kinds read as stopped — a stopped environment is not a failure, and
// must never render the failure glyph. A busy environment only reads busy once
// no sticky condition contradicts it: a stopped env whose last observation was
// busy is stopped, not busy.
function environmentStatusDot(envState: string, busy = false): StatusDotState {
  if (envState === ENV_STATE_FAILED) {
    return 'failed';
  }
  if (envState === ENV_STATE_STOPPED || envState === ENV_STATE_RUNTIME_STOPPED) {
    return 'stopped';
  }
  return busy ? 'busy' : 'running';
}

// EnvironmentIndicator is the one derived row state, computed from every input
// the sidebar has: the sticky condition, what the environment reports about
// itself, and whether the desktop owns tabs for it. It is the only thing this
// module exports about row state — the individual inputs are private, so a
// caller cannot render half of the derivation and disagree with the rest.
export interface EnvironmentIndicator {
  visible: boolean;
  dot: StatusDotState;
  // opened is true only when the desktop owns tabs, which is what makes the
  // indicator a Close control rather than a passive light. Reachability is
  // deliberately not the same thing: an env opened from the CLI is in use but
  // has no tabs here to close.
  opened: boolean;
  // condition names the environment, because it is read out of context — as the
  // indicator's accessible label and its tooltip.
  condition: string;
  // activity is the same state without the name, for the hover card, which is
  // already titled with the environment it describes.
  activity: string;
}

export interface EnvironmentIndicatorInputs {
  name: string;
  envState: string;
  isOpen: boolean;
  reachable: boolean;
  busy: boolean;
  detail: string;
}

export function environmentIndicator(raw: EnvironmentIndicatorInputs): EnvironmentIndicator {
  // The sticky condition describes a desktop session. Once the desktop holds no
  // tabs for the environment it no longer describes anything current, so it is
  // dropped rather than left to outlive the session that produced it — a closed
  // row must not keep flying a failure triangle for a session that is gone.
  const input = raw.isOpen ? raw : { ...raw, envState: '' };
  const dot = environmentStatusDot(input.envState, input.busy);
  // What keeps a closed row visible is the environment itself: its edge
  // answering, or work in flight. Otherwise there is nothing to report.
  const visible = input.isOpen || input.reachable || input.busy;
  return {
    visible,
    dot,
    opened: input.isOpen,
    condition: environmentCondition(input, dot),
    activity: environmentActivityLabel(input, dot),
  };
}

// environmentActivityLabel is the terse form. The card is a small surface and
// its heading already says which environment this is, so repeating the name on
// every row would crowd out the state itself.
function environmentActivityLabel(input: EnvironmentIndicatorInputs, dot: StatusDotState): string {
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

function environmentCondition(input: EnvironmentIndicatorInputs, dot: StatusDotState): string {
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
    // The case the blank row hid: reachable without desktop tabs. Say so, and
    // say why there is no Close affordance on it.
    return `${input.name} is running and in use elsewhere — not opened here`;
  }
  return `${input.name} is not open`;
}

// environmentStateRecovery names the action that gets the environment back, so
// the state is never shown without the way out of it. Empty for a running env.
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
