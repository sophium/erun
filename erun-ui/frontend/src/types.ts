export interface UIEnvironment {
  name: string;
  mcpUrl?: string;
  runtimeVersion?: string;
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
  kubernetesContext?: string;
  containerRegistry?: string;
  noGit?: boolean;
  setDefaultTenant?: boolean;
  action?: EnvironmentActionMode;
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

export interface UIVersionSuggestion {
  label: string;
  version: string;
  source?: string;
  image?: string;
}

export interface UIERunConfig {
  defaultTenant: string;
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
  runtimeVersion: string;
  sshd: UISSHDConfig;
  localPorts: UIEnvironmentLocalPorts;
  remote: boolean;
  snapshot: boolean;
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
