export type EnvironmentType = 'local-agent' | 'remote-agent' | 'runtime' | '';

// EnvironmentTypeValues lists the dropdown options. The model type for the
// `type` field below is widened to `string` because the Wails-generated
// binding (frontend/wailsjs/go/models.ts) widens the Go EnvironmentType
// alias to a bare `string` at the boundary — narrowing the field here would
// fail the LoadEnvironmentConfig/SaveEnvironmentConfig assignability check.
export const EnvironmentTypeValues: readonly EnvironmentType[] = [
  'local-agent',
  'remote-agent',
  'runtime',
] as const;

export interface UIEnvironment {
  name: string;
  type?: string;
  mcpUrl?: string;
  apiUrl?: string;
  runtimeVersion?: string;
  kubernetesContext?: string;
  isActive?: boolean;
  sshdEnabled?: boolean;
  remote: boolean;
  autoStart?: boolean;
}

// UIWorkingIssue mirrors the Go uiWorkingIssue read model returned by the
// EnvironmentWorkingIssue binding: the env worktree's current branch and, when
// the branch names an issue, its resolved title. `available` is false for
// remote-worktree envs whose branch can't be read from the host.
export interface UIWorkingIssue {
  available: boolean;
  branch?: string;
  issueNumber?: number;
  issueTitle?: string;
  reason?: string;
}

// UIEnvTrace mirrors the Go uiEnvTrace from the LoadEnvTrace binding: the
// Diagnostics console's erun-trace read model (issues #466/#508).
export interface UIEnvTrace {
  available: boolean;
  content?: string;
  path: string;
  reason?: string;
  notice?: string;
}

// UIUpgradeVersionCandidate mirrors the Go UpgradeVersionCandidate: one newer
// version an env's registries offered, tagged with the registry it came from
// (issue #527).
export interface UIUpgradeVersionCandidate {
  version: string;
  registry?: string;
}

// UIUpgradePlanItem mirrors the Go UpgradePlanItem from the ResolveUpgradePlan
// binding: one opted-in env's channel, current version, the latest version for
// that channel, and whether it lags (will be redeployed by Upgrade all).
export interface UIUpgradePlanItem {
  tenant: string;
  environment: string;
  channel: string;
  current: string;
  target: string;
  lagging: boolean;
  // The distinct newer versions discovered across the env's listed registries,
  // each with its source registry. One entry when a single target resolved;
  // more than one when the operator must pick (issue #527).
  candidates?: UIUpgradeVersionCandidate[];
  // Why target is empty (registry lookup failed, no published version for the
  // channel, or multiple newer candidates await a pick) — rendered under
  // "latest unknown" / the picker (issues #497, #527).
  unresolvedReason?: string;
}

export interface UITenant {
  name: string;
  defaultEnvironment?: string;
  cloudProviderAliases?: string[];
  primaryCloudProviderAlias?: string;
  environments: UIEnvironment[];
}

export type ManageTab = 'general' | 'runtime' | 'ai' | 'ports' | 'ssh' | 'history' | 'delete';
export type ManageEditTab = Exclude<ManageTab, 'delete'>;

export interface UISelection {
  tenant: string;
  environment: string;
  version?: string;
  runtimeImage?: string;
  runtimeCpu?: string;
  runtimeMemory?: string;
  kubernetesContext?: string;
  containerRegistry?: string;
  // type stays as bare string to match the Wails binding, which widens
  // the Go EnvironmentType alias at the boundary. Use the
  // EnvironmentType union for narrowed dropdown values.
  type?: string;
  localRepoPath?: string;
  noGit?: boolean;
  setDefaultTenant?: boolean;
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
  cloudProviders?: UICloudProviderStatus[];
}

export interface UITenantDashboardInput {
  tenant: string;
  environment?: string;
  apiUrl: string;
  mcpUrl?: string;
  kubernetesContext?: string;
  cloudProviderAlias: string;
}

export interface UITenantDashboard {
  tenant: string;
  environment?: string;
  apiUrl?: string;
  apiError?: string;
  apiLog?: string;
  apiLogError?: string;
  user?: UITenantDashboardUser;
  reviews?: UITenantDashboardReview[];
  mergeQueue?: UITenantDashboardReview[];
  builds?: UITenantDashboardBuild[];
  auditEvents?: UITenantDashboardAudit[];
  auditLogMessage?: string;
}

export interface UITenantDashboardUser {
  tenantId: string;
  userId: string;
  username?: string;
  roles?: string[];
  issuer?: string;
  subject?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface UITenantDashboardReview {
  reviewId: string;
  tenantId: string;
  name: string;
  targetBranch: string;
  sourceBranch: string;
  status: string;
  lastFailedBuildId?: string;
  lastReadyBuildId?: string;
  lastMergedBuildId?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface UITenantDashboardBuild {
  buildId: string;
  tenantId: string;
  reviewId: string;
  reviewName?: string;
  successful: boolean;
  commitId: string;
  version: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface UITenantDashboardAudit {
  type: string;
  actor?: string;
  action: string;
  createdAt?: string;
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
  // stopPendingSince is the RFC3339 timestamp at which the desktop
  // first saw stopEligible=true for this env. While set, the
  // auto-stop is "armed" — the real ec2:StopInstances will fire
  // after secondsUntilForcedStop more seconds unless cancelled or
  // activity resumes. Empty when no auto-stop is pending.
  stopPendingSince?: string;
  secondsUntilForcedStop?: number;
  gracePeriodSeconds?: number;
}

// UILastStopEvent describes one stop in the env's audit history.
// Loaded from the in-pod `idle_stop_history` MCP tool, which reads
// stop-history.json off the shared home PVC so the desktop sees
// both the in-pod monitor's auto-stops and the desktop's own
// manual stops. Surfaced in the env-config Manage dialog's History
// tab so the user can answer "why did my env stop?" without
// trawling logs.
//
// Source distinguishes auto-stops fired by the in-pod idle monitor
// from manual stops fired by the desktop's Stop button; the row
// renders a badge from this field. ArmedAt is the moment the grace
// window began — set on pod-monitor entries, empty on host-manual
// entries without prior armed grace. Policy is the resolved idle
// policy snapshot at fire time; older entries on disk that pre-date
// the snapshot leave it unset.
export interface UILastStopEvent {
  stoppedAt: string;
  armedAt?: string;
  graceSeconds: number;
  source?: string;
  reason: string;
  cloudContextName?: string;
  policy?: UIIdlePolicy;
  markers?: UILastStopMarker[];
}

export interface UILastStopMarker {
  name: string;
  idle: boolean;
  reason?: string;
  secondsIdleFor?: number;
}

// UIIdlePolicy mirrors the History-tab snapshot of the resolved
// idle policy from the Go side. TimeoutSeconds is rendered rather
// than a Go duration string so the frontend never parses "10m0s".
export interface UIIdlePolicy {
  timeoutSeconds: number;
  workingHours?: string;
  timezone?: string;
  idleTrafficBytes?: number;
}

export interface UIIdleMarker {
  name: string;
  idle: boolean;
  reason?: string;
  secondsRemaining?: number;
  clients?: UIIdleMarkerClient[];
}

// UIIdleMarkerClient is the per-IP detail row attached to a marker
// (currently only SSH). The desktop tooltip renders one of these
// under the marker line; CLI consumers of `activity status --json`
// see the same shape.
export interface UIIdleMarkerClient {
  address: string;
  bytes?: number;
  secondsAgo?: number;
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
  oidcIssuerUrl?: string;
  status: string;
  message?: string;
}

export interface UICloudProviderBearerToken {
  alias: string;
  issuer?: string;
  token: string;
  provider: UICloudProviderStatus;
}

export interface UIAWSCloudAliasInput {
  alias: string;
  username: string;
  accountId: string;
  profile: string;
  ssoRegion: string;
  ssoStartUrl: string;
  oidcIssuerUrl: string;
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
  stopProtection?: boolean;
  stopProtectionKnown?: boolean;
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
  apiUrl: string;
  cloudProviderAliases?: string[];
  primaryCloudProviderAlias?: string;
  cloudProviders?: UICloudProviderStatus[];
}

export interface UISSHDConfig {
  enabled: boolean;
  localPort: number;
  publicKeyPath: string;
  workspaceSyncEnabled: boolean;
  workspaceSyncLocalPath?: string;
  workspaceSyncStatus?: string;
  workspaceSyncStatusMessage?: string;
}

export interface UIEnvironmentLocalPorts {
  rangeStart: number;
  rangeEnd: number;
  mcp: number;
  api: number;
  ssh: number;
  contributeApp: number;
  mcpStatus: UIPortStatus;
  apiStatus: UIPortStatus;
  sshStatus: UIPortStatus;
  contributeAppStatus: UIPortStatus;
}

export interface UIPortStatus {
  available: boolean;
  status: string;
}

// UIContainerRegistryEntry mirrors the Go uiContainerRegistryEntry: one
// registry host plus the roles it carries (any of build/from/to/deploy).
export interface UIContainerRegistryEntry {
  registry: string;
  roles: string[];
}

export interface UIEnvironmentConfig {
  name: string;
  repoPath: string;
  kubernetesContext: string;
  containerRegistries: UIContainerRegistryEntry[];
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
  claude: UIEnvironmentClaudeConfig;
  claudeDefaults: UIEnvironmentClaudeDefaults;
  localPorts: UIEnvironmentLocalPorts;
  type?: string;
  localRepoPath?: string;
  remote: boolean;
  snapshot: boolean;
  // AutoStart is the desktop-only auto-start policy for the linked cloud
  // context: undefined means "ask the user once" (first-time prompt), true
  // means "always auto-start", false means "never auto-start; render the
  // titlebar Play button so the user can start manually".
  autoStart?: boolean;
  // RemoteHostCredentials toggles the per-env credential refresher: when on,
  // the desktop exports temporary AWS credentials from the cloud alias's host
  // profile and pushes them into the runtime pod's ~/.aws/credentials under
  // the erun-host profile, so SDK calls inside the pod act as the host
  // identity. Only meaningful for remote AWS-backed envs.
  remoteHostCredentials: boolean;
  // AutoUpgrade opts this env into the "Upgrade all" set; upgradeChannel
  // selects which release channel an upgrade targets ("stable" | "snapshot").
  // The Go side resolves an empty channel from the env type, so the loaded
  // value is always one of the two.
  autoUpgrade: boolean;
  upgradeChannel?: string;
  // DisableBuildScript makes `erun build` ignore any project build.sh for this
  // env and resolve Docker/release builds directly. Never reaches the pod.
  disableBuildScript: boolean;
}

export interface UIEnvironmentClaudeConfig {
  useMantle?: boolean;
  useBedrock?: boolean;
  models?: string[];
  maxOutputTokens?: number;
  effort?: string;
  defaultModel?: string;
  verboseDebug?: boolean;
}

export interface UIEnvironmentClaudeDefaults {
  useMantle: boolean;
  useBedrock: boolean;
  models: string[];
  maxOutputTokens: number;
  knownModels: string[];
  minTokens: number;
  maxTokens: number;
  effort: string;
  effortLevels: string[];
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
  slot?: number;
  kind?: string;
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
  reviewBase?: DiffReviewBase;
  reviewCommits?: DiffCommit[];
  scope?: 'current' | 'commit' | 'all';
  selectedCommit?: string;
  includesWorktree?: boolean;
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

export interface DiffReviewBase {
  branch?: string;
  commit?: string;
  shortCommit?: string;
}

export interface DiffCommit {
  hash: string;
  shortHash: string;
  subject: string;
  author: string;
  date: string;
}
