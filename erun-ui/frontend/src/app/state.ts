import type {
  DiffResult,
  EnvironmentActionMode,
  EnvironmentType,
  ManageTab,
  UICloudContextInitInput,
  UICloudProviderStatus,
  UIEnvironmentConfig,
  UIERunConfig,
  UIIdleStatus,
  UIRuntimeResourceStatus,
  UISelection,
  UITenant,
  UITenantConfig,
  UITenantDashboard,
  UIVersionSuggestion,
} from '@/types';

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
export const DEBUG_HEIGHT_STORAGE_KEY = 'erun.debugHeight';
export const PAST_TENANTS_STORAGE_KEY = 'erun.pastTenants';
export const PAST_ENVIRONMENTS_STORAGE_KEY = 'erun.pastEnvironments';
export const PAST_CONTAINER_REGISTRIES_STORAGE_KEY = 'erun.pastContainerRegistries';

export interface EnvironmentDialogState {
  open: boolean;
  actionMode: EnvironmentActionMode;
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
  busyAction: '' | 'save' | 'delete' | 'cloud-context-power';
  busyTarget: string;
  choicesOpen: boolean;
  error: string;
  pendingRedeploy: boolean;
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

export type TenantDashboardTab = 'users' | 'queue' | 'builds' | 'audit' | 'api-log';

export interface TenantDashboardState {
  tenant: string;
  tab: TenantDashboardTab;
  loading: boolean;
  error: string;
  data: UITenantDashboard | null;
}

export interface GlobalConfigDialogState {
  open: boolean;
  config: UIERunConfig;
  cloudContextDraft: UICloudContextInitInput;
  configLoading: boolean;
  busy: boolean;
  busyAction:
    | ''
    | 'save'
    | 'cloud-context-init'
    | 'cloud-context-power'
    | 'cloud-provider-init'
    | 'cloud-provider-login';
  busyTarget: string;
  error: string;
}

export interface AppNotification {
  kind: 'success' | 'warning' | 'error' | 'info';
  message: string;
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
  // Rolling buffer of the latest reconnect output lines. Capped by the slice
  // reducer to keep memory bounded under long deploys.
  lines: string[];
  error: string;
}

// Maximum number of reconnect output lines retained in ReconnectState.lines.
// The status surface shows a scrollable view; older lines beyond this cap drop
// off the top so the buffer can't grow unbounded across a long deploy.
export const RECONNECT_LINE_BUFFER_LIMIT = 200;

// AutoStartPromptState backs the first-time "Auto-start this environment?"
// dialog. The dialog opens when openSelection is asked to navigate to a remote
// env whose linked cloud context is stopped and whose env config does not yet
// record an AutoStart override. The user's answer is persisted via
// SetEnvironmentAutoStart so the prompt does not appear again unless the
// setting is reset from the manage-env dialog.
export interface AutoStartPromptState {
  open: boolean;
  selection: UISelection | null;
  saving: boolean;
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
  sidebarCloudAliasBusy: boolean;
  sidebarCloudAliasAction: '' | 'login' | 'logout' | 'bearer';
  debugOpen: boolean;
  debugHeight: number;
  debugOutput: string;
  lastDoctorBySelection: Record<string, DoctorOutcome>;
}

export const defaultEnvironmentDialog = (): EnvironmentDialogState => ({
  open: false,
  actionMode: 'init',
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

export const defaultTenantDashboard = (): TenantDashboardState => ({
  tenant: '',
  tab: 'users',
  loading: false,
  error: '',
  data: null,
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
  containerRegistry: '',
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
  remote: false,
  snapshot: true,
  remoteHostCredentials: false,
  autoUpgrade: false,
  upgradeChannel: 'stable',
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
  effort: 'max',
  effortLevels: ['low', 'medium', 'high', 'xhigh', 'max'],
});
