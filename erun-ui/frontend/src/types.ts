import type { UIContainerRegistryCluster } from './uiDiagnosticsTypes';
import type { UIEnvironmentActivity } from './uiEnvironmentActivityTypes';
import type { UIEnvironmentNodeSnapshot } from './uiEnvironmentNodeTypes';
import type { UIEnvironmentUsageSnapshot } from './uiEnvironmentUsageTypes';
import type { UIHostedEnvironment } from './uiHostedDefinitionTypes';
import type { UIHostGatewayDefaults, UIOpenRouterConfig } from './uiOpenRouterTypes';
import type {
  UIErunVersion,
  UIRuntimeImageLineMismatch,
  UIRuntimeVersionLine,
} from './uiRuntimeVersionLineTypes';

export type EnvironmentType = 'local-agent' | 'remote-agent' | 'runtime' | 'host' | '';

// `type` fields stay bare `string`: the Wails binding widens the Go alias.
export const EnvironmentTypeValues: readonly EnvironmentType[] = [
  'local-agent',
  'remote-agent',
  'runtime',
  'host',
] as const;

export interface UIEnvironment {
  name: string;
  type?: string;
  mcpUrl?: string;
  apiUrl?: string;
  runtimeVersion?: string;
  runtimeVersionLine?: UIRuntimeVersionLine;
  erunVersion?: UIErunVersion;
  runtimeImageLineMismatch?: UIRuntimeImageLineMismatch;
  kubernetesContext?: string;
  isActive?: boolean;
  sshdEnabled?: boolean;
  autoStart?: boolean;
  activity?: UIEnvironmentActivity;
  usage?: UIEnvironmentUsageSnapshot;
  // Absent when erun power-manages no cloud node for this env.
  node?: UIEnvironmentNodeSnapshot;
}

// `available` is false for remote-worktree envs whose branch the host can't read.
export interface UIWorkingIssue {
  available: boolean;
  branch?: string;
  issueNumber?: number;
  issueTitle?: string;
  reason?: string;
}

export interface UIEnvTrace {
  available: boolean;
  content?: string;
  path: string;
  reason?: string;
  notice?: string;
}

export interface UIAppLog {
  available: boolean;
  content?: string;
  path: string;
  reason?: string;
}

export interface UIUpgradeVersionCandidate {
  version: string;
  registry?: string;
}

export interface UIUpgradePlanItem {
  tenant: string;
  environment: string;
  channel: string;
  current: string;
  target: string;
  lagging: boolean;
  // More than one entry when the operator must pick between registries.
  candidates?: UIUpgradeVersionCandidate[];
  unresolvedReason?: string;
}

export interface UITenant {
  name: string;
  defaultEnvironment?: string;
  cloudProviderAliases?: string[];
  primaryCloudProviderAlias?: string;
  environments: UIEnvironment[];
}

export type ManageTab =
  | 'general'
  | 'runtime'
  | 'ai'
  | 'ports'
  | 'ssh'
  | 'jobs'
  | 'history'
  | 'delete';
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
  clusterRegistry?: boolean;
  // Mutually exclusive with clusterRegistry and containerRegistry.
  erunRegistry?: boolean;
  type?: string;
  localRepoPath?: string;
  noGit?: boolean;
  setDefaultTenant?: boolean;
  components?: string[];
}

export interface UIDeployableComponent {
  name: string;
  runtime: boolean;
  // 'local-chart' or 'published-chart'.
  source: string;
  // Saved deploy.components, else the repo plan, else the runtime alone.
  selected: boolean;
  // Empty until the version-aware backend resolves it.
  publishedChart?: string;
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
  versionSuggestionNotices?: UIVersionSuggestionNotice[];
  cloudProviders?: UICloudProviderStatus[];
  // Set when config files were unreadable, so empty tenants is not the empty state.
  configUnreadable?: boolean;
}

export interface UIIdleStatus {
  timeoutSeconds: number;
  secondsUntilStop: number;
  stopEligible: boolean;
  outsideWorkingHours: boolean;
  managedCloud: boolean;
  // False means assembled on the host because the pod was unreachable; may be stale.
  fromPod: boolean;
  stopBlockedReason?: string;
  stopError?: string;
  cloudContextName?: string;
  cloudContextStatus?: string;
  cloudContextLabel?: string;
  markers?: UIIdleMarker[];
  // Orchestrator/CLI claims only; this desktop's own sessions never take one.
  leases?: UIEnvironmentLease[];
  // Set while auto-stop is armed; it fires after secondsUntilForcedStop.
  stopPendingSince?: string;
  secondsUntilForcedStop?: number;
  gracePeriodSeconds?: number;
}

// UILastStopEvent merges in-pod auto-stops with the desktop's manual stops.
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

// timeoutSeconds is a number so the frontend never parses Go durations.
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

export interface UIIdleMarkerClient {
  address: string;
  bytes?: number;
  secondsAgo?: number;
}

export interface UIEnvironmentLease {
  name: string;
  secondsHeld?: number;
  jobId?: string;
}

export interface UIVersionSuggestion {
  label: string;
  version: string;
  source?: string;
  image?: string;
}

// kind is 'auth' (private image) or 'unreachable'.
export interface UIVersionSuggestionNotice {
  image: string;
  kind: string;
}

export interface UIVersionSuggestions {
  suggestions: UIVersionSuggestion[];
  notices?: UIVersionSuggestionNotice[];
}

export interface UIERunConfig {
  defaultTenant: string;
  cloudProviders?: UICloudProviderStatus[];
  cloudContexts?: UICloudContextStatus[];
  openRouter?: UIOpenRouterConfig;
  // Read from the operator's Claude settings; never stored until they save.
  openRouterDefaults?: UIHostGatewayDefaults;
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

// Must match erun-common's CloudProvider* constants.
export const CloudProviderAWS = 'aws';
export const CloudProviderCloudflare = 'cloudflare';
export const CloudProviderERun = 'erun';

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

// Exactly one of `registry` and `cluster` is set.
export interface UIContainerRegistryEntry {
  registry: string;
  cluster?: UIContainerRegistryCluster;
  roles: string[];
}

export interface UIEnvironmentConfig {
  name: string;
  repoPath: string;
  kubernetesContext: string;
  containerRegistries: UIContainerRegistryEntry[];
  // True when resolved from the project's .erun/config.yaml rather than the env.
  containerRegistriesInherited: boolean;
  cloudProviderAlias: string;
  cloudProviderAliases?: string[];
  cloudAliasSlots?: UIEnvironmentCloudAlias[];
  cloudContext?: UICloudContextStatus;
  runtimeVersion: string;
  // Empty means the chart published with the deployed version.
  runtimeChart?: string;
  // Where erun's own artifacts resolve from, not where project images push.
  runtimeRegistry?: string;
  imagePullSecrets?: string[];
  runtimePod: UIRuntimePodConfig;
  sshd: UISSHDConfig;
  idle: {
    timeout: string;
    workingHours: string;
    idleTrafficBytes: number;
  };
  claude: UIEnvironmentClaudeConfig;
  claudeDefaults: UIEnvironmentClaudeDefaults;
  // A save that changes it reopens AI tabs.
  aiLaunchSignature: string;
  localPorts: UIEnvironmentLocalPorts;
  type?: string;
  localRepoPath?: string;
  // undefined: ask once; true: always; false: never (manual Play button).
  autoStart?: boolean;
  // Deprecated: kept for binding assignability; nothing reads it.
  remoteHostCredentials: boolean;
  // The Go side resolves an empty upgradeChannel, so it is always set on load.
  autoUpgrade: boolean;
  upgradeChannel?: string;
  // Saving raises the pending-redeploy banner.
  disableBuildScript: boolean;
  // Saving raises the pending-redeploy banner.
  platformAccount: boolean;
  // No-op without repoURL. Saving either raises the pending-redeploy banner.
  mountSource: boolean;
  repoURL: string;
  deployComponents?: string[];
  hosted?: UIHostedEnvironment;
}

export interface UIEnvironmentClaudeConfig {
  useMantle?: boolean;
  useBedrock?: boolean;
  // undefined inherits the operator's decision.
  useGateway?: boolean;
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
  gatewayConfigured: boolean;
  // models/knownModels stay the env's own; the AI tab picks by draft useGateway.
  gatewayModels: string[];
  gatewayDefaultModel: string;
}

export interface UIRuntimePodConfig {
  cpu: string;
  memory: string;
}

// schedulable is what the scheduler admits by requests; worstCase assumes every
// container hits its limit. Readings are instantaneous, not ceilings.
export interface UIRuntimeResourceStatus {
  kubernetesContext: string;
  available: boolean;
  message?: string;
  node?: string;
  schedulable: UIRuntimeResourceReading;
  // False makes the free figure an upper bound (an unparseable request).
  schedulableComplete: boolean;
  worstCase: UIRuntimeResourceReading;
  floored: boolean;
  measuredUsage: boolean;
  unmeasuredContainers?: number;
  unreadableRequests?: number;
  nodes?: UIRuntimeResourceNode[];
}

export interface UIRuntimeResourceReading {
  cpu: UIRuntimeResourceMetric;
  memory: UIRuntimeResourceMetric;
  message?: string;
  notice?: string;
}

export interface UIRuntimeResourceMetric {
  total: number;
  used: number;
  free: number;
  unit: string;
  formatted: string;
  floored: boolean;
}

export interface UIRuntimeResourceNode {
  name: string;
  schedulable: UIRuntimeResourceReading;
  schedulableComplete: boolean;
  worstCase: UIRuntimeResourceReading;
}

export interface StartSessionResult {
  sessionId: number;
  selection: UISelection;
  slot?: number;
  kind?: string;
  // True for a background orchestration: no Local tab; progress goes to the queue.
  orchestrated?: boolean;
  // Set when an unconfirmed start found the env occupied; sessionId is 0.
  occupancy?: UIEnvironmentLease[];
}

export interface TerminalOutputPayload {
  sessionId: number;
  data: string;
}

export interface TerminalExitPayload {
  sessionId: number;
  reason?: string;
  // Marks an exit the desktop itself caused, not a failure.
  deliberate?: boolean;
}

export interface PastedFileResult {
  path: string;
}

export interface AgentOutputEntry {
  name: string;
  path: string;
  size: number;
  modTime: string;
  isDir: boolean;
}

export interface AgentOutputsList {
  dir: string;
  entries: AgentOutputEntry[];
  total: number;
  truncated: boolean;
}

// Split out to stay under eslint max-lines; re-exported for `from './types'`.
export * from './diffTypes';
export * from './reviewTypes';
export * from './tenantDashboardTypes';
export * from './uiExposureTypes';

// exitCode is null unless exited, so a missing outcome never reads as zero.
export interface UIEnvironmentJob {
  id: string;
  name: string;
  state: string;
  kind?: string;
  agentTool?: string;
  command?: string[];
  dir?: string;
  exitCode: number | null;
  startedAtUnix?: number;
  endedAtUnix?: number;
  progress?: UIEnvironmentJobProgress;
}

export interface UIEnvironmentJobProgress {
  activity?: string;
  lastMessage?: string;
  turns: number;
  toolsRun: number;
}

export interface UIEnvironmentJobOutput {
  job: UIEnvironmentJob;
  offset: number;
  nextOffset: number;
  output: string;
  hasMore: boolean;
  complete: boolean;
}
