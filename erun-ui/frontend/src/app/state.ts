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
  UIEnvironmentServiceList,
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

import type { AppNotification, AppNotificationKind } from './model/appNotification';
import type { GlobalConfigCloudProviderBusyAction } from './model/globalConfigCloudProviderBusyAction';

export type { AppNotification, AppNotificationKind };
import type { ReachabilityKind } from './reconnectCopy';
import type { TenantDashboardState } from './reviewDetailState';

export * from './reviewDetailState';

export const MIN_SIDEBAR_WIDTH = 248;
export const MAX_SIDEBAR_WIDTH = 520;
export const DEFAULT_SIDEBAR_WIDTH = 338;
export const MIN_REVIEW_WIDTH = 420;
export const MAX_REVIEW_WIDTH = 1400;
export const DEFAULT_REVIEW_WIDTH = 620;
// MIN_MAIN_PANE_WIDTH is the narrowest usable <main> beside the review panel.
export const MIN_MAIN_PANE_WIDTH = 360;
// MIN_DASHBOARD_PANE_WIDTH is the dashboard's measured unshrinkable width
// (~495px) rounded up; the sidebar breakpoint is sized off this larger floor.
export const MIN_DASHBOARD_PANE_WIDTH = 500;
export const GRID_DIVIDER_WIDTH = 10;

export function computeMaxReviewWidth(
  viewportWidth: number,
  effectiveSidebarWidth: number,
): number {
  const fittable = viewportWidth - effectiveSidebarWidth - MIN_MAIN_PANE_WIDTH - GRID_DIVIDER_WIDTH;
  // Floor at 0, not MIN_REVIEW_WIDTH, so the panel never exceeds the viewport.
  return Math.max(0, Math.min(MAX_REVIEW_WIDTH, fittable));
}

// computeMaxSidebarWidth reserves MIN_DASHBOARD_PANE_WIDTH since the sidebar
// cannot tell which <main> content is showing.
export function computeMaxSidebarWidth(viewportWidth: number): number {
  return Math.max(
    MIN_SIDEBAR_WIDTH,
    Math.min(MAX_SIDEBAR_WIDTH, viewportWidth - MIN_DASHBOARD_PANE_WIDTH - GRID_DIVIDER_WIDTH),
  );
}

// effectiveSidebarWidth is the rendered width (0 when collapsed); use it instead
// of layout.sidebarWidth.
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

export const SIDEBAR_COLLAPSE_BREAKPOINT =
  MIN_SIDEBAR_WIDTH + GRID_DIVIDER_WIDTH + MIN_DASHBOARD_PANE_WIDTH;
// Hysteresis so a window parked on the breakpoint doesn't oscillate.
export const SIDEBAR_COLLAPSE_HYSTERESIS = 40;
// Below this no user override can keep the sidebar open.
export const SIDEBAR_HARD_COLLAPSE_WIDTH = MIN_SIDEBAR_WIDTH + GRID_DIVIDER_WIDTH;

// null means no explicit choice, so the viewport threshold governs.
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
export * from './storageKeys';

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
  // clusterRegistry is the detected in-cluster erun-registry, or null.
  clusterRegistry: UIClusterRegistryStatus | null;
  useClusterRegistry: boolean;
  // useErunRegistry selects registry.erunpaas.com/<tenant>; exclusive with the other two.
  useErunRegistry: boolean;
  // hostedRegistry is null while the probe is in flight.
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
  // Dialog-owned: the tenants slice is rewritten for the sidebar-selected env.
  versionSuggestions: UIVersionSuggestion[];
  versionSuggestionNotices: UIVersionSuggestionNotice[];
  deployComponents: UIDeployableComponent[];
  deployComponentSelection: string[];
  deployComponentsLoading: boolean;
  // null until the version-aware probe answers.
  runtimeChartPlan: UIRuntimeChartPlan | null;
  health: UIEnvironmentHealth | null;
  healthLoading: boolean;
  exposures: UIExposureList;
  exposuresLoading: boolean;
  environmentServices: UIEnvironmentServiceList;
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
  erunApiUrlDraft: string;
  configLoading: boolean;
  busy: boolean;
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
  // Empty when status === 'idle'.
  tenant: string;
  environment: string;
  kind: ReachabilityKind;
  lines: string[];
  error: string;
}

export const RECONNECT_LINE_BUFFER_LIMIT = 200;

// AutoStartPromptState backs the first-time "Auto-start this environment?" dialog.
export interface AutoStartPromptState {
  open: boolean;
  selection: UISelection | null;
  saving: boolean;
  error: string;
}

export interface AIOccupancyPendingStart {
  key: string;
  selection: UISelection;
  slot: number;
  cols: number;
  rows: number;
  label: string;
}

// AIOccupancyPromptState backs the "an agent is already here" dialog; confirming
// is deliberate, never an automatic retry.
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
  environmentServices: { configured: false, restricted: false, services: [] },
  exposeForm: { service: '', backendService: '', targetIP: '', port: '' },
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
  aiLaunchSignature: '',
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
  // False until the backend reports it, so gateway controls never flash.
  gatewayConfigured: false,
  gatewayModels: [],
  gatewayDefaultModel: '',
  models: ['sonnet', 'haiku'],
  maxOutputTokens: 4096,
  knownModels: ['opus', 'sonnet', 'haiku', 'fable'],
  minTokens: 1,
  maxTokens: 200000,
  effort: 'ultracode',
  effortLevels: ['low', 'medium', 'high', 'xhigh', 'max', 'ultracode'],
});
