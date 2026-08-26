// Diagnostics read-model types (in-cluster registry block, cluster-registry
// probe, environment health check) live in ./uiDiagnosticsTypes, imported by
// name where needed. This file only needs the cluster block for the entry below.
import type { UIContainerRegistryCluster } from './uiDiagnosticsTypes';
import type { UIEnvironmentActivity } from './uiEnvironmentActivityTypes';

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
  activity?: UIEnvironmentActivity;
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

// UIAppLog is the Diagnostics console's read model for the desktop's own
// durable log — evidence for an orchestrator or app-level fault, neither of
// which has an env trace to fall back on.
export interface UIAppLog {
  available: boolean;
  content?: string;
  path: string;
  reason?: string;
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
  // Selects the in-cluster erun-registry instead of the containerRegistry string.
  clusterRegistry?: boolean;
  // erunRegistry selects erun's hosted registry for the tenant. Mutually
  // exclusive with both clusterRegistry and containerRegistry.
  erunRegistry?: boolean;
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
  // The chart a by-reference deploy installs for this item at the chosen version.
  // For the runtime it is the tenant's own <tenant>-devops chart when published,
  // else the canonical erun-devops fallback; empty until the version-aware backend
  // resolves it.
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
  // apiError is a whole-dashboard failure (the caller's identity could not be
  // read). A single panel's own failure lives on that panel.
  apiError?: string;
  apiLog?: string;
  apiLogError?: string;
  user?: UITenantDashboardUser;
  reviews?: UITenantDashboardReview[];
  mergeQueue?: UITenantDashboardReview[];
  builds?: UITenantDashboardBuild[];
  auditEvents?: UITenantDashboardAudit[];
  panels?: UITenantDashboardPanel[];
  // canCreateReview and canAdvanceMergeQueue report whether the signed-in user
  // may attempt those writes at all, so the composing actions can be hidden
  // rather than rendered to fail on submit.
  canCreateReview: boolean;
  canAdvanceMergeQueue: boolean;
}

// UITenantDashboardPanel is one panel's own outcome: `restricted` names the API
// read the signed-in user lacks, so a panel they may not see is never rendered
// as an empty one.
export interface UITenantDashboardPanel {
  tab: string;
  restricted?: string;
  error?: string;
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
  // fromPod is true only when this reading came from the pod's own idle
  // monitor over MCP. False means it was assembled on the host because the
  // pod could not be reached, so the countdown it carries may be stale.
  fromPod: boolean;
  stopBlockedReason?: string;
  stopError?: string;
  cloudContextName?: string;
  cloudContextStatus?: string;
  cloudContextLabel?: string;
  markers?: UIIdleMarker[];
  // Leases are the work claims currently holding the environment (an
  // orchestrator or CLI job) — not this desktop's own interactive AI/ERun/Local
  // sessions, which never take one. A non-empty list is a coexisting agent the
  // AI tab does not manage.
  leases?: UIEnvironmentLease[];
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

// UIEnvironmentLease is one activity lease held on the environment — a named
// job, not this desktop's own interactive session. secondsHeld is precomputed
// so the renderer never redoes the time math.
export interface UIEnvironmentLease {
  name: string;
  secondsHeld?: number;
  // Set only when a job holds the lease, so a surface that names the occupancy
  // can also act on it. Absent for every other holder.
  jobId?: string;
}

export interface UIVersionSuggestion {
  label: string;
  version: string;
  source?: string;
  image?: string;
}

// UIVersionSuggestionNotice explains why a runtime-image source produced no
// version suggestions. kind is 'auth' (private image — the operator must log in)
// or 'unreachable' (registry/network failure).
export interface UIVersionSuggestionNotice {
  image: string;
  kind: string;
}

// UIVersionSuggestions is the version picker read model returned by
// LoadVersionSuggestions: deployable choices plus per-source notices.
export interface UIVersionSuggestions {
  suggestions: UIVersionSuggestion[];
  notices?: UIVersionSuggestionNotice[];
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

// UIContainerRegistryEntry is one registry plus the roles it carries (any of
// build/from/to/deploy). It names its target either as a static `registry`
// host or as a context-resolved `cluster` block (exactly one is set).
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
  // True when the shown registries are resolved from the project's
  // .erun/config.yaml (a local-agent env with no env-level override) rather than
  // carried on the env config. The editor marks them inherited-from-project.
  containerRegistriesInherited: boolean;
  cloudProviderAlias: string;
  cloudProviderAliases?: string[];
  // One entry per provider type (aws, cloudflare) that has a configured alias
  // or a current attachment.
  cloudAliasSlots?: UIEnvironmentCloudAlias[];
  cloudContext?: UICloudContextStatus;
  runtimeVersion: string;
  // runtimeChart names the chart this env's runtime is installed from, as an OCI
  // reference that may carry its own version. Empty means the chart published
  // with the deployed version -- right whenever chart and image ride one line.
  runtimeChart?: string;
  // Where the environment resolves erun's own artifacts from -- the runtime
  // chart and platform images -- as distinct from where this project's images
  // are pushed.
  runtimeRegistry?: string;
  // The Kubernetes dockerconfigjson secrets the runtime pod pulls its image
  // with. Without one, a private runtime image leaves the pod unable to start.
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
  // platformAccount binds the env's runtime ServiceAccount to cluster-admin so
  // in-pod platform Terraform (the cluster edge) and component installs can
  // manage cluster-scoped resources. Changing it changes the RBAC a redeploy
  // renders, so saving raises the pending-redeploy banner.
  platformAccount: boolean;
  // mountSource opts a runtime env into a writable source worktree the pod clones
  // at the deployed release ref; repoURL is the git remote it clones. Runtime
  // envs only, and a no-op without repoURL. Changing either alters what a
  // redeploy provisions, so saving raises the pending-redeploy banner.
  mountSource: boolean;
  repoURL: string;
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

// A reading of node capacity taken at one instant, not a fixed ceiling. `notice`
// explains what the number alone cannot: `floored` means the maximum equals what
// this env already holds because the node is full, and `unmeasuredContainers`
// counts capacity the reading cannot see.
export interface UIRuntimeResourceStatus {
  kubernetesContext: string;
  available: boolean;
  message?: string;
  notice?: string;
  node?: string;
  floored: boolean;
  measuredUsage: boolean;
  unmeasuredContainers?: number;
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
  floored: boolean;
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
  // Occupancy lists the leases already holding the environment when an
  // unconfirmed AI session start found it occupied: sessionId is 0 and no
  // session was started. Retry with confirmed=true to start anyway.
  occupancy?: UIEnvironmentLease[];
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

// Diff view types live in ./diffTypes to keep this file under eslint's max-lines
// cap; re-exported here so `from './types'` keeps resolving them.
export * from './diffTypes';

// Review-detail types live in ./reviewTypes for the same reason.
export * from './reviewTypes';

// A retained job in an environment's job store. exitCode is null unless the job
// reached exited, so a missing outcome is never read as a successful zero, and
// progress is absent for a command job rather than a fabricated zero state.
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
