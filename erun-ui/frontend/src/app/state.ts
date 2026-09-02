import type {
  DiffResult,
  EnvironmentType,
  ExposeServiceFormState,
  ManageTab,
  UICloudContextInitInput,
  UICloudProviderStatus,
  UIDeployableComponent,
  UIEnvironmentConfig,
  UIEnvironmentLease,
  UIERunConfig,
  UIExposureList,
  UIIdleStatus,
  UIRuntimeResourceStatus,
  UISelection,
  UITenant,
  UITenantConfig,
  UIVersionSuggestion,
  UIVersionSuggestionNotice,
} from '@/types';
import type {
  UIClusterRegistryStatus,
  UIEnvironmentHealth,
  UIHostedRegistryStatus,
} from '@/uiDiagnosticsTypes';
import type { UIRuntimeChartPlan } from '@/uiRuntimeChartTypes';

import type { AppNotificationAction } from './model/appNotificationAction';
import type { GlobalConfigCloudProviderBusyAction } from './model/globalConfigCloudProviderBusyAction';
import type { ReachabilityKind } from './reconnectCopy';
import type { TenantDashboardState } from './reviewDetailState';

export * from './reviewDetailState';

export const MIN_SIDEBAR_WIDTH = 248;
export const MAX_SIDEBAR_WIDTH = 520;
export const DEFAULT_SIDEBAR_WIDTH = 338;
export const MIN_REVIEW_WIDTH = 420;
export const MAX_REVIEW_WIDTH = 1400;
export const DEFAULT_REVIEW_WIDTH = 620;
// MIN_MAIN_PANE_WIDTH is the narrowest <main> the shell treats as usable
// while showing the terminal/diff-review surface — the only other content
// that competes with the review panel for the same viewport, so they can't
// independently agree to starve it.
export const MIN_MAIN_PANE_WIDTH = 360;
// MIN_DASHBOARD_PANE_WIDTH is the narrowest <main> the shell treats as usable
// while showing the tenant dashboard, which never renders alongside the
// review panel (MainPane.tsx shows one or the other), so it is its own,
// wider floor rather than folded into MIN_MAIN_PANE_WIDTH above. Measured,
// not guessed: the dashboard's tab strip (Users/Reviews/Merge queue/Builds/
// Audit log/API log) plus the page header's padding has an unwrapping,
// unshrinkable content width of ~495px in Chromium. MIN_MAIN_PANE_WIDTH's
// 360 let that content render wider than <main>, invisibly — the
// intermediate overflow-x-auto wrapper (MainPane.tsx) permits the overflow
// but never paints a discoverable scrollbar for it. 500 rounds the
// measurement up with a small margin for cross-platform font metrics. The
// sidebar breakpoint below is sized off this, the larger of the two floors,
// since the sidebar competes with whichever of the two is showing.
export const MIN_DASHBOARD_PANE_WIDTH = 500;
export const GRID_DIVIDER_WIDTH = 10;

export function computeMaxReviewWidth(
  viewportWidth: number,
  effectiveSidebarWidth: number,
): number {
  const fittable = viewportWidth - effectiveSidebarWidth - MIN_MAIN_PANE_WIDTH - GRID_DIVIDER_WIDTH;
  // Floor at 0, not MIN_REVIEW_WIDTH: a floor at the panel's own minimum
  // forced it wider than the viewport had room for whenever fittable went
  // negative, which is the starved-<main> overflow this module prevents.
  return Math.max(0, Math.min(MAX_REVIEW_WIDTH, fittable));
}

// Mirrors computeMaxReviewWidth for the sidebar. Unlike the review panel, its
// floor stays at MIN_SIDEBAR_WIDTH — collapsing away entirely is the separate
// decision nextSidebarHidden below makes, not a width squeeze. Reserves room
// for MIN_DASHBOARD_PANE_WIDTH (not the smaller MIN_MAIN_PANE_WIDTH) because
// the sidebar has no visibility into which of the two <main> contents is
// showing, so it must leave enough for the wider requirement either way.
export function computeMaxSidebarWidth(viewportWidth: number): number {
  return Math.max(
    MIN_SIDEBAR_WIDTH,
    Math.min(MAX_SIDEBAR_WIDTH, viewportWidth - MIN_DASHBOARD_PANE_WIDTH - GRID_DIVIDER_WIDTH),
  );
}

// What the shell actually renders for the sidebar column: 0 while collapsed,
// otherwise the stored width reclamped to the viewport. Every consumer that
// needs the sidebar's real current width (the review-panel max-width calc,
// the CSS var writer) goes through this instead of layout.sidebarWidth
// directly, so a resize is reflected everywhere at once.
export function effectiveSidebarWidth(
  hidden: boolean,
  width: number,
  viewportWidth: number,
): number {
  if (hidden) {
    return 0;
  }
  return Math.min(computeMaxSidebarWidth(viewportWidth), Math.max(MIN_SIDEBAR_WIDTH, width));
}

// Below this viewport width, the sidebar at its own minimum plus the divider
// already leaves <main> under MIN_DASHBOARD_PANE_WIDTH, so the shell
// collapses the sidebar instead of continuing to squeeze it.
export const SIDEBAR_COLLAPSE_BREAKPOINT =
  MIN_SIDEBAR_WIDTH + GRID_DIVIDER_WIDTH + MIN_DASHBOARD_PANE_WIDTH;
// A widen has to clear the breakpoint by this margin before the sidebar
// auto-reopens, so a window parked on the breakpoint doesn't oscillate.
export const SIDEBAR_COLLAPSE_HYSTERESIS = 40;
// Below this width even a collapsed sidebar's own divider has nowhere to sit
// beside a non-empty <main>; no user override can keep it open past this.
export const SIDEBAR_HARD_COLLAPSE_WIDTH = MIN_SIDEBAR_WIDTH + GRID_DIVIDER_WIDTH;

// Distinguishes the operator's explicit sidebar choice from the shell's
// automatic viewport-driven collapse. null means no explicit choice yet, so
// the automatic threshold governs; once the operator toggles the sidebar,
// their choice sticks across future resizes until they toggle it again.
export type SidebarUserOverride = 'shown' | 'hidden' | null;

export function nextSidebarHidden(
  current: boolean,
  userOverride: SidebarUserOverride,
  viewportWidth: number,
): boolean {
  if (viewportWidth < SIDEBAR_HARD_COLLAPSE_WIDTH) {
    return true;
  }
  if (userOverride !== null) {
    return userOverride === 'hidden';
  }
  if (viewportWidth < SIDEBAR_COLLAPSE_BREAKPOINT) {
    return true;
  }
  if (viewportWidth >= SIDEBAR_COLLAPSE_BREAKPOINT + SIDEBAR_COLLAPSE_HYSTERESIS) {
    return false;
  }
  return current;
}
export const MIN_FILES_WIDTH = 220;
export const MAX_FILES_WIDTH = 460;
export const DEFAULT_FILES_WIDTH = 300;
export const MIN_DEBUG_HEIGHT = 120;
export const MAX_DEBUG_HEIGHT = 520;
export const DEFAULT_DEBUG_HEIGHT = 220;
export const SIDEBAR_WIDTH_STORAGE_KEY = 'erun.sidebarWidth';
export const REVIEW_WIDTH_STORAGE_KEY = 'erun.reviewWidth';
export const FILES_WIDTH_STORAGE_KEY = 'erun.filesWidth';
export const FILES_OPEN_STORAGE_KEY = 'erun.filesOpen';
export const DEBUG_OPEN_STORAGE_KEY = 'erun.debugOpen';
// Opt-in, and deliberately OFF by default: xterm's screenReaderMode makes the
// AccessibilityManager rewrite the same hidden helper textarea that captures
// keystrokes, so a focus transition mid-line made the next input event re-emit
// the whole accumulated buffer into the pty (#1335). It stays available for
// anyone who needs it; it must not be imposed on everyone who types.
export const TERMINAL_SCREEN_READER_MODE_STORAGE_KEY = 'erun.terminal.screenReaderMode';
export const DEBUG_HEIGHT_STORAGE_KEY = 'erun.debugHeight';
export const THEME_STORAGE_KEY = 'erun.theme';

export type ThemePreference = 'light' | 'dark';
export const PAST_TENANTS_STORAGE_KEY = 'erun.pastTenants';
export const PAST_ENVIRONMENTS_STORAGE_KEY = 'erun.pastEnvironments';
export const PAST_CONTAINER_REGISTRIES_STORAGE_KEY = 'erun.pastContainerRegistries';

export interface EnvironmentDialogState {
  open: boolean;
  tenant: string;
  environment: string;
  version: string;
  kubernetesContext: string;
  kubernetesContexts: string[];
  kubernetesContextsLoading: boolean;
  resourceStatus: UIRuntimeResourceStatus | null;
  resourceStatusLoading: boolean;
  runtimePod: {
    cpu: string;
    memory: string;
  };
  containerRegistry: string;
  // clusterRegistry is the in-cluster erun-registry detected for the selected
  // Kubernetes context (null when none / not yet resolved). When present and
  // useClusterRegistry is set, the env is created with a resolvable cluster:
  // registry entry instead of the containerRegistry string.
  clusterRegistry: UIClusterRegistryStatus | null;
  useClusterRegistry: boolean;
  // useErunRegistry selects erun's hosted registry (registry.erunpaas.com/<tenant>),
  // authenticated by the tenant's own API token. Exclusive with the other two.
  useErunRegistry: boolean;
  // hostedRegistry is the reachability probe result for erun's hosted
  // registry, checked once when the dialog opens (the host is fixed, not
  // context-dependent like clusterRegistry). Null while the check is still in
  // flight; useErunRegistry only takes effect once it resolves available.
  hostedRegistry: UIHostedRegistryStatus | null;
  envType: EnvironmentType;
  localRepoPath: string;
  noGit: boolean;
  setDefaultTenant: boolean;
  versionImage: string;
  choicesOpen: boolean;
  busy: boolean;
  error: string;
}

export interface ManageDialogState {
  open: boolean;
  tab: ManageTab;
  selection: UISelection | null;
  version: string;
  versionImage: string;
  config: UIEnvironmentConfig;
  initialConfig: UIEnvironmentConfig | null;
  configLoading: boolean;
  resourceStatus: UIRuntimeResourceStatus | null;
  resourceStatusLoading: boolean;
  confirmation: string;
  busy: boolean;
  busyAction: '' | 'save' | 'delete' | 'stop' | 'cloud-context-power';
  busyTarget: string;
  choicesOpen: boolean;
  error: string;
  pendingRedeploy: boolean;
  // Version suggestions are dialog-owned, not read from the shared tenants slice:
  // that slice is (re)written by boot and every environment-change delta for the
  // sidebar-selected env, which would clobber this dialog's env-specific picker
  // (e.g. showing only the upstream fallback while a tenant build fires deltas).
  versionSuggestions: UIVersionSuggestion[];
  versionSuggestionNotices: UIVersionSuggestionNotice[];
  deployComponents: UIDeployableComponent[];
  deployComponentSelection: string[];
  deployComponentsLoading: boolean;
  // Which chart a deploy of the picked version would install. Dialog-owned like
  // the suggestions above, and null until the version-aware probe answers.
  runtimeChartPlan: UIRuntimeChartPlan | null;
  // Result of the General tab's "Check environment" health run, null until the
  // operator runs it. healthLoading gates the in-flight indicator.
  health: UIEnvironmentHealth | null;
  healthLoading: boolean;
  // The Ports tab's public-exposure surface (issue #1351). Exposures is loaded
  // automatically alongside the rest of the dialog's read models, matching
  // deployComponents; exposeForm/exposeBusy/exposeError track the "Expose a
  // service" form, and unexposeConfirming/unexposeBusy/unexposeError track the
  // two-step "Remove public access" confirm below the list.
  exposures: UIExposureList;
  exposuresLoading: boolean;
  exposeForm: ExposeServiceFormState;
  exposeBusy: boolean;
  exposeError: string;
  unexposeConfirming: boolean;
  unexposeBusy: boolean;
  unexposeError: string;
}

export interface TenantDialogState {
  open: boolean;
  tenant: string;
  config: UITenantConfig;
  configLoading: boolean;
  busy: boolean;
  busyAction: '' | 'save' | 'cloud-oidc';
  busyTarget: string;
  error: string;
}

export interface GlobalConfigDialogState {
  open: boolean;
  config: UIERunConfig;
  cloudContextDraft: UICloudContextInitInput;
  // erunApiUrlDraft is the "Add erun platform" popover's own input — the only
  // field InitERunCloudProvider needs; everything else is discovered from the
  // platform itself.
  erunApiUrlDraft: string;
  configLoading: boolean;
  busy: boolean;
  // 'cloud-provider-init' is the AWS add action; 'cloud-provider-cloudflare-init' is Cloudflare;
  // 'cloud-provider-erun-init' is the erun platform add action.
  busyAction:
    | ''
    | 'save'
    | 'cloud-context-init'
    | 'cloud-context-power'
    | 'cloud-provider-init'
    | 'cloud-provider-cloudflare-init'
    | 'cloud-provider-erun-init'
    | GlobalConfigCloudProviderBusyAction;
  busyTarget: string;
  error: string;
}

export interface AppNotification {
  // Unique per queued entry so a specific one can be dismissed (auto-dismiss
  // timer, explicit dismiss click) without disturbing sibling entries queued
  // before or after it.
  id: string;
  kind: 'success' | 'warning' | 'error' | 'info';
  message: string;
  // Optional tags so a notification can be dismissed later by the state that raised it.
  tenant?: string;
  environment?: string;
  source?: string;
  // orchestratorId is the orchestrator-scoped analogue of tenant/environment,
  // set when the notification's action operates on a specific orchestrator
  // (e.g. restarting it) rather than a specific env.
  orchestratorId?: string;
  // Action names a control TitlebarStatus can render beside the message that
  // performs the message's own remedy directly -- see AppNotificationAction.
  // Undefined means no action.
  action?: AppNotificationAction;
}

export type TerminalStatusKind = 'info' | 'warning' | 'error';
export type TerminalStatusAction = '' | 'wait-longer';

export type TerminalTabKind =
  | 'local'
  | 'erun'
  | 'ai'
  | 'extra'
  | 'contribute-erun'
  | 'contribute-ai';

export interface TerminalTab {
  sessionId: number;
  slot: number;
  kind: TerminalTabKind;
  label: string;
}

export interface SSHDInitOutcome {
  ranAt: number;
  success: boolean;
  message: string;
}

export interface DoctorOutcome {
  ranAt: number;
  success: boolean;
  message: string;
}

export type ReconnectStatus = 'idle' | 'confirm' | 'running' | 'error';

export interface ReconnectState {
  status: ReconnectStatus;
  // tenant + environment scope the in-flight reconnect so other envs can stay
  // interactive while this one is running. Empty when status === 'idle'.
  tenant: string;
  environment: string;
  // Which reachability shape triggered this reconnect, so the dialog and
  // status panel can say "Open" for a stopped environment and "Reconnect" for
  // a stale forward instead of one fixed script for both (#1230).
  kind: ReachabilityKind;
  // Rolling buffer of the latest reconnect output lines, not the full transcript.
  lines: string[];
  error: string;
}

// Caps ReconnectState.lines so the buffer can't grow unbounded across a long deploy.
export const RECONNECT_LINE_BUFFER_LIMIT = 200;

// AutoStartPromptState backs the first-time "Auto-start this environment?" dialog,
// shown when opening a remote env whose cloud context is stopped and that has no
// AutoStart choice recorded yet. The answer is persisted so the prompt does not
// reappear unless the setting is reset.
export interface AutoStartPromptState {
  open: boolean;
  selection: UISelection | null;
  saving: boolean;
  error: string;
}

// AIOccupancyPendingStart carries what a confirmed retry needs to actually
// start the session and record its tab — captured at the moment the
// unconfirmed start reported the environment occupied.
export interface AIOccupancyPendingStart {
  key: string;
  selection: UISelection;
  slot: number;
  cols: number;
  rows: number;
  label: string;
}

// AIOccupancyPromptState backs the "an agent is already here" dialog shown
// when starting the AI tab finds the environment held by another job's
// activity lease. Confirming is a deliberate "start a second agent anyway",
// never an automatic retry.
export interface AIOccupancyPromptState {
  open: boolean;
  leases: UIEnvironmentLease[];
  pending: AIOccupancyPendingStart | null;
  starting: boolean;
  error: string;
}

export interface AppState {
  tenants: UITenant[];
  cloudProviders: UICloudProviderStatus[];
  selected: UISelection | null;
  versionSuggestions: UIVersionSuggestion[];
  environmentDialog: EnvironmentDialogState;
  manageDialog: ManageDialogState;
  tenantDialog: TenantDialogState;
  tenantDashboard: TenantDashboardState;
  globalConfigDialog: GlobalConfigDialogState;
  autoStartPrompt: AutoStartPromptState;
  aiOccupancyPrompt: AIOccupancyPromptState;
  collapsedTenants: Set<string>;
  sessionId: number;
  tabsByEnv: Record<string, TerminalTab[]>;
  selectedSessionByEnv: Record<string, number>;
  sidebarWidth: number;
  reviewWidth: number;
  filesWidth: number;
  filesOpen: boolean;
  sidebarHidden: boolean;
  reviewOpen: boolean;
  changedFilesOpen: boolean;
  diff: DiffResult | null;
  diffLoading: boolean;
  diffError: string;
  diffErrorReconnectable: boolean;
  reconnect: ReconnectState;
  selectedDiffPath: string;
  selectedReviewScope: 'current' | 'commit' | 'all';
  selectedReviewCommit: string;
  diffFilter: string;
  collapsedDiffDirs: Set<string>;
  notification: AppNotification | null;
  terminalMessage: string;
  terminalStatusKind: TerminalStatusKind;
  terminalStatusDetail: string;
  terminalStatusAction: TerminalStatusAction;
  terminalBusy: boolean;
  terminalCopyOutput: string;
  terminalCopyStatus: string;
  idleStatus: UIIdleStatus | null;
  sidebarCloudAliasBusyByAlias: Record<string, '' | 'login' | 'logout' | 'bearer'>;
  debugOpen: boolean;
  debugHeight: number;
  lastDoctorBySelection: Record<string, DoctorOutcome>;
  lastSSHDInitBySelection: Record<string, SSHDInitOutcome>;
}

export const defaultEnvironmentDialog = (): EnvironmentDialogState => ({
  open: false,
  tenant: '',
  environment: '',
  version: '',
  kubernetesContext: '',
  kubernetesContexts: [],
  kubernetesContextsLoading: false,
  resourceStatus: null,
  resourceStatusLoading: false,
  runtimePod: defaultRuntimePodConfig(),
  containerRegistry: '',
  clusterRegistry: null,
  useClusterRegistry: false,
  useErunRegistry: false,
  hostedRegistry: null,
  envType: 'remote-agent',
  localRepoPath: '',
  noGit: false,
  setDefaultTenant: true,
  versionImage: '',
  choicesOpen: false,
  busy: false,
  error: '',
});

export const defaultManageDialog = (): ManageDialogState => ({
  open: false,
  tab: 'general',
  selection: null,
  version: '',
  versionImage: '',
  config: defaultEnvironmentConfig(),
  initialConfig: null,
  configLoading: false,
  resourceStatus: null,
  resourceStatusLoading: false,
  confirmation: '',
  busy: false,
  busyAction: '',
  busyTarget: '',
  choicesOpen: false,
  error: '',
  pendingRedeploy: false,
  versionSuggestions: [],
  versionSuggestionNotices: [],
  deployComponents: [],
  deployComponentSelection: [],
  deployComponentsLoading: false,
  runtimeChartPlan: null,
  health: null,
  healthLoading: false,
  exposures: { configured: false, restricted: false, services: [] },
  exposuresLoading: false,
  exposeForm: { service: '', targetIP: '', port: '' },
  exposeBusy: false,
  exposeError: '',
  unexposeConfirming: false,
  unexposeBusy: false,
  unexposeError: '',
});

export const defaultTenantDialog = (): TenantDialogState => ({
  open: false,
  tenant: '',
  config: defaultTenantConfig(),
  configLoading: false,
  busy: false,
  busyAction: '',
  busyTarget: '',
  error: '',
});

export const defaultGlobalConfigDialog = (): GlobalConfigDialogState => ({
  open: false,
  config: defaultERunConfig(),
  cloudContextDraft: defaultCloudContextInitInput(),
  erunApiUrlDraft: '',
  configLoading: false,
  busy: false,
  busyAction: '',
  busyTarget: '',
  error: '',
});

export const defaultAutoStartPrompt = (): AutoStartPromptState => ({
  open: false,
  selection: null,
  saving: false,
  error: '',
});

export const defaultAIOccupancyPrompt = (): AIOccupancyPromptState => ({
  open: false,
  leases: [],
  pending: null,
  starting: false,
  error: '',
});

export const defaultERunConfig = (): UIERunConfig => ({
  defaultTenant: '',
  cloudProviders: [],
  cloudContexts: [],
});

export const defaultCloudContextInitInput = (): UICloudContextInitInput => ({
  name: '',
  cloudProviderAlias: '',
  region: 'eu-west-2',
  instanceType: 'c8gd.2xlarge',
  diskType: 'gp3',
  diskSizeGb: 100,
});

export const defaultTenantConfig = (): UITenantConfig => ({
  name: '',
  defaultEnvironment: '',
  apiUrl: '',
  cloudProviderAliases: [],
  primaryCloudProviderAlias: '',
  cloudProviders: [],
});

export const defaultEnvironmentConfig = (): UIEnvironmentConfig => ({
  name: '',
  repoPath: '',
  kubernetesContext: '',
  containerRegistries: [],
  containerRegistriesInherited: false,
  cloudProviderAlias: '',
  runtimeVersion: '',
  runtimePod: defaultRuntimePodConfig(),
  sshd: {
    enabled: false,
    localPort: 0,
    publicKeyPath: '',
    workspaceSyncEnabled: false,
    workspaceSyncLocalPath: '',
    workspaceSyncStatus: '',
    workspaceSyncStatusMessage: '',
  },
  idle: {
    timeout: '5m0s',
    workingHours: '08:00-20:00',
    idleTrafficBytes: 0,
  },
  claude: {},
  claudeDefaults: defaultClaudeDefaults(),
  localPorts: {
    rangeStart: 0,
    rangeEnd: 0,
    mcp: 0,
    api: 0,
    ssh: 0,
    contributeApp: 0,
    mcpStatus: {
      available: false,
      status: '',
    },
    apiStatus: {
      available: false,
      status: '',
    },
    sshStatus: {
      available: false,
      status: '',
    },
    contributeAppStatus: {
      available: false,
      status: '',
    },
  },
  type: 'local-agent',
  remoteHostCredentials: false,
  autoUpgrade: false,
  upgradeChannel: 'stable',
  runtimeChart: '',
  disableBuildScript: false,
  platformAccount: false,
  mountSource: false,
  repoURL: '',
});

export const defaultRuntimePodConfig = (): { cpu: string; memory: string } => ({
  cpu: '4',
  memory: '8.7',
});

export const defaultClaudeDefaults = (): UIEnvironmentConfig['claudeDefaults'] => ({
  useMantle: false,
  useBedrock: false,
  models: ['sonnet', 'haiku'],
  maxOutputTokens: 4096,
  knownModels: ['opus', 'sonnet', 'haiku', 'fable'],
  minTokens: 1,
  maxTokens: 200000,
  effort: 'ultracode',
  effortLevels: ['low', 'medium', 'high', 'xhigh', 'max', 'ultracode'],
});
