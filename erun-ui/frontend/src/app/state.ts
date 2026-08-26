import type {
  DiffResult,
  EnvironmentType,
  ManageTab,
  UICloudContextInitInput,
  UICloudProviderStatus,
  UIDeployableComponent,
  UIEnvironmentConfig,
  UIEnvironmentLease,
  UIERunConfig,
  UIIdleStatus,
  UIRuntimeResourceStatus,
  UISelection,
  UITenant,
  UITenantConfig,
  UIVersionSuggestion,
  UIVersionSuggestionNotice,
} from '@/types';
import type { UIClusterRegistryStatus, UIEnvironmentHealth } from '@/uiDiagnosticsTypes';
import type { UIRuntimeChartPlan } from '@/uiRuntimeChartTypes';

import type { ReachabilityKind } from './reconnectCopy';
import type { TenantDashboardState } from './reviewDetailState';

export * from './reviewDetailState';

export const MIN_SIDEBAR_WIDTH = 248;
export const MAX_SIDEBAR_WIDTH = 520;
export const DEFAULT_SIDEBAR_WIDTH = 338;
export const MIN_REVIEW_WIDTH = 420;
export const MAX_REVIEW_WIDTH = 1400;
export const DEFAULT_REVIEW_WIDTH = 620;
const REVIEW_GRID_TERMINAL_MIN_WIDTH = 360;
const REVIEW_GRID_DIVIDER_WIDTH = 10;

export function computeMaxReviewWidth(
  viewportWidth: number,
  effectiveSidebarWidth: number,
): number {
  const fittable =
    viewportWidth -
    effectiveSidebarWidth -
    REVIEW_GRID_TERMINAL_MIN_WIDTH -
    REVIEW_GRID_DIVIDER_WIDTH;
  return Math.max(MIN_REVIEW_WIDTH, Math.min(MAX_REVIEW_WIDTH, fittable));
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
  configLoading: boolean;
  busy: boolean;
  // 'cloud-provider-init' is the AWS add action; 'cloud-provider-cloudflare-init' is Cloudflare.
  busyAction:
    | ''
    | 'save'
    | 'cloud-context-init'
    | 'cloud-context-power'
    | 'cloud-provider-init'
    | 'cloud-provider-cloudflare-init'
    | 'cloud-provider-login';
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
  // Action names a control TitlebarStatus can render beside the message that
  // performs the message's own remedy directly — currently only 'deploy'
  // (open the tagged env's deploy dialog). Undefined means no action (#1390).
  action?: 'deploy';
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
  idleCloudContextBusy: boolean;
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
