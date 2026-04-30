export interface UIEnvironment {
  name: string;
  mcpUrl?: string;
  runtimeVersion?: string;
  isActive?: boolean;
  sshdEnabled?: boolean;
}

export interface UITenant {
  name: string;
  environments: UIEnvironment[];
}

export type EnvironmentActionMode = 'init' | 'deploy';
export type ManageTab = 'deploy' | 'config' | 'delete';

export interface UISelection {
  tenant: string;
  environment: string;
  version?: string;
  runtimeImage?: string;
  runtimeCpu?: string;
  runtimeMemory?: string;
  kubernetesContext?: string;
  containerRegistry?: string;
  noGit?: boolean;
  bootstrap?: boolean;
  setDefaultTenant?: boolean;
  action?: EnvironmentActionMode;
  debug?: boolean;
}

export interface UIBuildDetails {
  version: string;
  commit?: string;
  date?: string;
}

export interface UIState {
  tenants: UITenant[];
  selected?: UISelection;
  message?: string;
  build?: UIBuildDetails;
  versionSuggestions?: UIVersionSuggestion[];
  kubernetesContexts?: string[];
}

export interface UIIdleStatus {
  timeoutSeconds: number;
  secondsUntilStop: number;
  stopEligible: boolean;
  outsideWorkingHours: boolean;
  managedCloud: boolean;
  stopBlockedReason?: string;
  stopError?: string;
  cloudContextName?: string;
  cloudContextStatus?: string;
  cloudContextLabel?: string;
  markers?: UIIdleMarker[];
}

export interface UIIdleMarker {
  name: string;
  idle: boolean;
  reason?: string;
  secondsRemaining?: number;
}

export interface UIVersionSuggestion {
  label: string;
  version: string;
  source?: string;
  image?: string;
}

export interface UIERunConfig {
  defaultTenant: string;
  cloudProviders?: UICloudProviderStatus[];
  cloudContexts?: UICloudContextStatus[];
}

export interface UICloudProviderStatus {
  alias: string;
  provider: string;
  username?: string;
  accountId?: string;
  profile?: string;
  status: string;
  message?: string;
}

export interface UIAWSCloudAliasInput {
  alias: string;
  username: string;
  accountId: string;
  profile: string;
  ssoRegion: string;
  ssoStartUrl: string;
}

export interface UICloudContextStatus {
  name: string;
  provider: string;
  cloudProviderAlias: string;
  region: string;
  instanceId?: string;
  publicIp?: string;
  instanceType: string;
  diskType: string;
  diskSizeGb: number;
  kubernetesContext: string;
  status: string;
  message?: string;
}

export interface UICloudContextInitInput {
  name: string;
  cloudProviderAlias: string;
  region: string;
  instanceType: string;
  diskType: string;
  diskSizeGb: number;
}

export interface UITenantConfig {
  name: string;
  defaultEnvironment: string;
}

export interface UISSHDConfig {
  enabled: boolean;
  localPort: number;
  publicKeyPath: string;
}

export interface UIEnvironmentLocalPorts {
  rangeStart: number;
  rangeEnd: number;
  mcp: number;
  ssh: number;
  mcpStatus: UIPortStatus;
  sshStatus: UIPortStatus;
}

export interface UIPortStatus {
  available: boolean;
  status: string;
}

export interface UIEnvironmentConfig {
  name: string;
  repoPath: string;
  kubernetesContext: string;
  containerRegistry: string;
  cloudProviderAlias: string;
  cloudProviderAliases?: string[];
  cloudContext?: UICloudContextStatus;
  runtimeVersion: string;
  runtimePod: UIRuntimePodConfig;
  sshd: UISSHDConfig;
  idle: {
    timeout: string;
    workingHours: string;
    idleTrafficBytes: number;
  };
  localPorts: UIEnvironmentLocalPorts;
  remote: boolean;
  snapshot: boolean;
}

export interface UIRuntimePodConfig {
  cpu: string;
  memory: string;
}

export interface UIRuntimeResourceStatus {
  kubernetesContext: string;
  available: boolean;
  message?: string;
  cpu: UIRuntimeResourceMetric;
  memory: UIRuntimeResourceMetric;
  nodes?: UIRuntimeResourceNode[];
}

export interface UIRuntimeResourceMetric {
  total: number;
  used: number;
  free: number;
  unit: string;
  formatted: string;
}

export interface UIRuntimeResourceNode {
  name: string;
  cpu: UIRuntimeResourceMetric;
  memory: UIRuntimeResourceMetric;
}

export interface StartSessionResult {
  sessionId: number;
  selection: UISelection;
}

export interface TerminalOutputPayload {
  sessionId: number;
  data: string;
}

export interface TerminalExitPayload {
  sessionId: number;
  reason?: string;
}

export interface PastedImageResult {
  path: string;
}

export interface DeleteEnvironmentResult {
  tenant: string;
  environment: string;
  namespace?: string;
  kubernetesContext?: string;
  namespaceDeleteError?: string;
  cloudContextStopError?: string;
}

export interface DiffResult {
  workingDirectory?: string;
  rawDiff: string;
  summary: DiffSummary;
  files?: DiffFile[];
  tree?: DiffTreeNode[];
}

export interface DiffSummary {
  fileCount: number;
  additions: number;
  deletions: number;
}

export interface DiffFile {
  path: string;
  oldPath?: string;
  newPath?: string;
  status: string;
  additions: number;
  deletions: number;
  binary?: boolean;
  hunks?: DiffHunk[];
}

export interface DiffHunk {
  header: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  lines?: DiffLine[];
}

export interface DiffLine {
  kind: 'context' | 'add' | 'delete' | 'meta';
  content: string;
  oldLine?: number;
  newLine?: number;
}

export interface DiffTreeNode {
  name: string;
  path: string;
  parentPath?: string;
  type: 'directory' | 'file';
  depth: number;
  status?: string;
  additions?: number;
  deletions?: number;
}
