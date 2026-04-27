import type {
  DiffResult,
  EnvironmentActionMode,
  ManageTab,
  UIERunConfig,
  UIEnvironmentConfig,
  UICloudContextInitInput,
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
export const SIDEBAR_WIDTH_STORAGE_KEY = 'erun.sidebarWidth';
export const REVIEW_WIDTH_STORAGE_KEY = 'erun.reviewWidth';
export const FILES_WIDTH_STORAGE_KEY = 'erun.filesWidth';
export const FILES_OPEN_STORAGE_KEY = 'erun.filesOpen';

export interface EnvironmentDialogState {
  open: boolean;
  actionMode: EnvironmentActionMode;
  tenant: string;
  environment: string;
  version: string;
  kubernetesContext: string;
  kubernetesContexts: string[];
  kubernetesContextsLoading: boolean;
  containerRegistry: string;
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
  configLoading: boolean;
  confirmation: string;
  busy: boolean;
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
  error: string;
}

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
  diff: DiffResult | null;
  diffLoading: boolean;
  diffError: string;
  selectedDiffPath: string;
  diffFilter: string;
  collapsedDiffDirs: Set<string>;
  terminalMessage: string;
  terminalCopyOutput: string;
  terminalCopyStatus: string;
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
  containerRegistry: 'erunpaas',
  noGit: false,
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
  confirmation: '',
  busy: false,
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
});

export const defaultEnvironmentConfig = (): UIEnvironmentConfig => ({
  name: '',
  repoPath: '',
  kubernetesContext: '',
  containerRegistry: '',
  cloudProviderAlias: '',
  runtimeVersion: '',
  sshd: {
    enabled: false,
    localPort: 0,
    publicKeyPath: '',
  },
  localPorts: {
    rangeStart: 0,
    rangeEnd: 0,
    mcp: 0,
    ssh: 0,
    mcpStatus: {
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
