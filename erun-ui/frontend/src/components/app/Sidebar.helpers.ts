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
  // busyFromEnvironment says the label describes the environment's own
  // condition rather than an operation this desktop is running. A row's spinner
  // needs the label either way, because a screen reader has no other context;
  // a surface that already names the environment does not, and repeating
  // "<env> is busy — X" inside a card headed "<env>" says the same thing twice
  // while displacing the prose the indicator already owns.
  busyFromEnvironment: boolean;
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
  envObserved: boolean,
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
  return {
    selected,
    busy,
    busyLabel,
    busyFromEnvironment,
    isLocal,
    runtimeVersion: environment?.runtimeVersion?.trim() ?? '',
    selection: { tenant: tenantName, environment: environmentName },
  };
}

// The five reasons a row spins, kept out of deriveEnvironmentRow so that
// function stays under the complexity gate — and so the set is one named thing
// rather than a disjunction growing inside a larger function.
// The environment's own answer both adds and subtracts. Adding was already
// wired: work started from a terminal or over MCP spins a row the desktop never
// launched anything on. Subtracting was not, and that is the half that matters
// for a row nobody can clear — every other input here is desktop-local, set when
// this desktop starts something and cleared when it sees it end, so any path
// that loses the ending leaves a latch with nothing to release it. One row span
// for six hours while `erun idle` reported every marker idle.
//
// An observation reporting no work is therefore allowed to clear those latches.
// It has to be the environment's own answer, not merely a reachable port: an
// edge that has wedged behind a port that still accepts connections reports no
// work because nobody asked it, and letting that clear a latch would trade a row
// that never stops for one that stops while the work is still running.
//
// isOpening and reconnecting stay authoritative regardless. They are this
// desktop's own in-flight operations, begun a moment ago in response to a click,
// and the environment cannot yet have observed what has not reached it.
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

// The operations this desktop is running itself, in the order that decides
// which one names the row. Split out from the label so a caller can ask whether
// there is one at all without re-deriving the precedence.
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
//
// A forward outage reads as a failure wherever it appears, including on a row
// the desktop never opened: the environment is unreachable to every client of
// it, which is the opposite of the quiet row it would otherwise render as.
function environmentStatusDot(envState: string, busy = false, outage = false): StatusDotState {
  if (envState === ENV_STATE_FAILED || outage) {
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
  // outage is the environment having lost the port-forward it had, past the
  // point a repair could bring it back. It is deliberately separate from
  // reachable: reachable is what the row already believed, and believing it is
  // what let a dead environment render as a running one — while a dropped
  // forward is not reachable either, and rendered as one nobody had opened.
  outage: boolean;
  busy: boolean;
  detail: string;
}

export function environmentIndicator(raw: EnvironmentIndicatorInputs): EnvironmentIndicator {
  // The sticky condition describes a desktop session. Once the desktop holds no
  // tabs for the environment it no longer describes anything current, so it is
  // dropped rather than left to outlive the session that produced it — a closed
  // row must not keep flying a stale stopped ring for a session that is gone.
  //
  // A failed deploy is the one exception: it is not a property of the session
  // that observed it, it is a property of the environment (its runtime is
  // broken), and closing the tabs that watched it fail does not fix it. As long
  // as the environment stays unreachable, the row must keep naming the failure
  // — the alternative is a closed, failed environment going silently blank,
  // indistinguishable from one nobody has ever opened. Reachability still wins
  // once it comes back: an environment that answers again is reported on its
  // own terms (below), not by a stale flag from before it recovered.
  const keepFailedWhenClosed = raw.envState === ENV_STATE_FAILED && !raw.reachable;
  const input = raw.isOpen || keepFailedWhenClosed ? raw : { ...raw, envState: '' };
  const dot = environmentStatusDot(input.envState, input.busy, input.outage);
  // What keeps a closed row visible is the environment itself: its edge
  // answering, work in flight, a failure it cannot report any other way once
  // its session is gone, or an outage.
  const visible = input.isOpen || input.reachable || input.busy || input.outage || dot === 'failed';
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

// FORWARD_OUTAGE_RECOVERY names the way out of a broken forward, so the state is
// never shown without it (Nielsen #9). Deploying is the recovery rather than
// re-opening because the desktop already re-opened it — repeatedly, and it did
// not help, which is the only reason this state is rendered at all.
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
