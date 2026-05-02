import type {
  DiffResult,
  EnvironmentActionMode,
  ManageTab,
  UIERunConfig,
  UIEnvironmentConfig,
  UIIdleStatus,
  UICloudContextInitInput,
  UIRuntimeResourceStatus,
  UISelection,
  UITenantConfig,
  UITenant,
  UIVersionSuggestion,
} from '@/types';

export const MIN_SIDEBAR_WIDTH = 248;
export const MAX_SIDEBAR_WIDTH = 520;
export const DEFAULT_SIDEBAR_WIDTH = 338;
export const MIN_REVIEW_WIDTH = 420;
export const MAX_REVIEW_WIDTH = 920;
export const DEFAULT_REVIEW_WIDTH = 620;
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
  noGit: boolean;
  bootstrap: boolean;
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
  configLoading: boolean;
  resourceStatus: UIRuntimeResourceStatus | null;
  resourceStatusLoading: boolean;
  confirmation: string;
  busy: boolean;
  busyAction: '' | 'save' | 'delete' | 'cloud-context-power';
  busyTarget: string;
  choicesOpen: boolean;
  error: string;
}

export interface TenantDialogState {
  open: boolean;
  tenant: string;
  config: UITenantConfig;
  configLoading: boolean;
  busy: boolean;
  error: string;
}

export interface GlobalConfigDialogState {
  open: boolean;
  config: UIERunConfig;
  cloudContextDraft: UICloudContextInitInput;
  configLoading: boolean;
  busy: boolean;
  busyAction: '' | 'save' | 'cloud-context-init' | 'cloud-context-power' | 'cloud-provider-init' | 'cloud-provider-login';
  busyTarget: string;
  error: string;
}

export interface AppNotification {
  kind: 'success' | 'warning' | 'error' | 'info';
  message: string;
}

export type TerminalStatusKind = 'info' | 'warning' | 'error';
export type TerminalStatusAction = '' | 'wait-longer';

export interface AppState {
  tenants: UITenant[];
  selected: UISelection | null;
  versionSuggestions: UIVersionSuggestion[];
  environmentDialog: EnvironmentDialogState;
  manageDialog: ManageDialogState;
  tenantDialog: TenantDialogState;
  globalConfigDialog: GlobalConfigDialogState;
  collapsedTenants: Set<string>;
  sessionId: number;
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
  debugOpen: boolean;
  debugHeight: number;
  debugOutput: string;
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
  containerRegistry: 'erunpaas',
  noGit: false,
  bootstrap: false,
  setDefaultTenant: true,
  versionImage: '',
  choicesOpen: false,
  busy: false,
  error: '',
});

export const defaultManageDialog = (): ManageDialogState => ({
  open: false,
  tab: 'config',
  selection: null,
  version: '',
  versionImage: '',
  config: defaultEnvironmentConfig(),
  configLoading: false,
  resourceStatus: null,
  resourceStatusLoading: false,
  confirmation: '',
  busy: false,
  busyAction: '',
  busyTarget: '',
  choicesOpen: false,
  error: '',
});

export const defaultTenantDialog = (): TenantDialogState => ({
  open: false,
  tenant: '',
  config: defaultTenantConfig(),
  configLoading: false,
  busy: false,
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
  },
  idle: {
    timeout: '5m0s',
    workingHours: '08:00-20:00',
    idleTrafficBytes: 0,
  },
  localPorts: {
    rangeStart: 0,
    rangeEnd: 0,
    mcp: 0,
    api: 0,
    ssh: 0,
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
  },
  remote: false,
  snapshot: true,
});

export const defaultRuntimePodConfig = (): { cpu: string; memory: string } => ({
  cpu: '4',
  memory: '8.7',
});
