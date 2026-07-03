export type EnvironmentType = 'local-agent' | 'remote-agent' | 'runtime' | '';

// EnvironmentTypeValues are the narrowed dropdown options; the `type` field
// below must stay bare `string` because the Wails binding widens the Go
// EnvironmentType alias, so narrowing it would break assignability.
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
  autoStart?: boolean;
}

// UIWorkingIssue is the env worktree's current branch and, when the branch
// names an issue, its resolved title. `available` is false for remote-worktree
// envs whose branch can't be read from the host.
export interface UIWorkingIssue {
  available: boolean;
  branch?: string;
  issueNumber?: number;
  issueTitle?: string;
  reason?: string;
}

// UIEnvTrace is the Diagnostics console's erun-trace read model.
export interface UIEnvTrace {
  available: boolean;
  content?: string;
  path: string;
  reason?: string;
  notice?: string;
}

// UIUpgradeVersionCandidate is one newer version an env's registries offered,
// tagged with the registry it came from.
export interface UIUpgradeVersionCandidate {
  version: string;
  registry?: string;
}

// UIUpgradePlanItem is one opted-in env's upgrade state: its channel, current
// and latest versions, and whether it lags (and so is redeployed by Upgrade all).
export interface UIUpgradePlanItem {
  tenant: string;
  environment: string;
  channel: string;
  current: string;
  target: string;
  lagging: boolean;
  // One entry when a single target resolved; more than one when the operator
  // must pick between registries.
  candidates?: UIUpgradeVersionCandidate[];
  // Why target is empty: registry lookup failed, no published version for the
  // channel, or multiple newer candidates await a pick.
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
  // Bare string to match the Wails binding, which widens the Go EnvironmentType
  // alias; use the EnvironmentType union for narrowed dropdown values.
  type?: string;
  localRepoPath?: string;
  noGit?: boolean;
  setDefaultTenant?: boolean;
  // The operator's one-shot deploy selection from the Runtime tab checklist:
  // chart directory names plus the runtime release name.
  components?: string[];
}

// UIDeployableComponent is one selectable deploy target for an environment
// (the Runtime tab's "Components to deploy" checklist).
export interface UIDeployableComponent {
  name: string;
  runtime: boolean;
  // 'local-chart' when a repo-local chart backs the component, or
  // 'published-chart' for the runtime when only the published erun-devops chart
  // is available.
  source: string;
  // The env's current resolved default selection: saved deploy.components, else
  // the repo plan, else the runtime alone.
  selected: boolean;
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
  // RFC3339 timestamp when the desktop first saw stopEligible=true. While set,
  // the auto-stop is "armed": ec2:StopInstances fires after
  // secondsUntilForcedStop more seconds unless cancelled or activity resumes.
  // Empty when no auto-stop is pending.
  stopPendingSince?: string;
  secondsUntilForcedStop?: number;
  gracePeriodSeconds?: number;
}

// UILastStopEvent is one stop in the env's audit history, merging the in-pod
// monitor's auto-stops with the desktop's own manual stops so the user can
// answer "why did my env stop?".
//
// source distinguishes auto-stops from manual stops. armedAt is empty on
// host-manual entries without prior armed grace. policy is the idle-policy
// snapshot at fire time; entries that pre-date the snapshot leave it unset.
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

// UIIdlePolicy is the History-tab snapshot of the resolved idle policy.
// timeoutSeconds is a number, not a Go duration string, so the frontend never
// parses "10m0s".
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

// UIIdleMarkerClient is the per-IP detail row attached to a marker (currently
// only SSH); the same shape backs the CLI's `activity status --json`.
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

// Canonical cloud provider type strings; must match erun-common's
// CloudProviderAWS / CloudProviderCloudflare.
export const CloudProviderAWS = 'aws';
export const CloudProviderCloudflare = 'cloudflare';

// UIEnvironmentCloudAlias is one provider-type slot in the env's cloud-alias
// view: the alias attached for that type (empty when none) and the aliases the
// operator can choose from.
export interface UIEnvironmentCloudAlias {
  provider: string;
  alias: string;
  options: string[];
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

// UIContainerRegistryEntry is one registry host plus the roles it carries (any
// of build/from/to/deploy).
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
  // One entry per provider type (aws, cloudflare) that has a configured alias
  // or a current attachment.
  cloudAliasSlots?: UIEnvironmentCloudAlias[];
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
  // Desktop-only auto-start policy for the linked cloud context: undefined means
  // "ask the user once", true "always auto-start", false "never auto-start; show
  // the titlebar Play button for manual start".
  autoStart?: boolean;
  // Deprecated: host AWS credential delivery now follows whether an AWS cloud
  // alias is attached, not this toggle. Retained only to stay assignable to the
  // generated Go binding; nothing reads it.
  remoteHostCredentials: boolean;
  // autoUpgrade opts this env into the "Upgrade all" set. upgradeChannel is the
  // targeted release channel ("stable" | "snapshot"); the Go side resolves an
  // empty channel from the env type, so a loaded value is always one of the two.
  autoUpgrade: boolean;
  upgradeChannel?: string;
  // Makes `erun build` ignore the project build.sh and build Docker/release
  // images directly. Changes how a redeploy rebuilds the runtime image, so
  // saving it raises the pending-redeploy banner.
  disableBuildScript: boolean;
  // The per-machine saved deploy selection; empty means "no saved selection".
  deployComponents?: string[];
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
  // True when the call started a background command orchestration (e.g. an
  // agent-env build→push→deploy) instead of a foreground PTY session: there is
  // no Local tab to activate, so progress and completion surface through the
  // activity queue.
  orchestrated?: boolean;
}

export interface TerminalOutputPayload {
  sessionId: number;
  data: string;
}

export interface TerminalExitPayload {
  sessionId: number;
  reason?: string;
}

export interface PastedFileResult {
  path: string;
}

// AgentOutputEntry is one file or folder an agent produced in the runtime pod's
// outputs directory.
export interface AgentOutputEntry {
  name: string;
  path: string;
  size: number;
  modTime: string;
  isDir: boolean;
}

// AgentOutputsList is the entries in the pod's outputs directory, newest-first.
export interface AgentOutputsList {
  dir: string;
  entries: AgentOutputEntry[];
  total: number;
  truncated: boolean;
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
