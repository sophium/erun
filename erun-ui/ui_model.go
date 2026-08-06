package main

import eruncommon "github.com/sophium/erun/erun-common"

type uiState struct {
	Tenants                  []uiTenant              `json:"tenants"`
	Selected                 *uiSelection            `json:"selected,omitempty"`
	Message                  string                  `json:"message,omitempty"`
	Build                    uiBuildDetails          `json:"build"`
	VersionSuggestions       []uiVersion             `json:"versionSuggestions,omitempty"`
	VersionSuggestionNotices []uiVersionNotice       `json:"versionSuggestionNotices,omitempty"`
	CloudProviders           []uiCloudProviderStatus `json:"cloudProviders,omitempty"`
}

type uiTenant struct {
	Name                      string          `json:"name"`
	DefaultEnvironment        string          `json:"defaultEnvironment,omitempty"`
	CloudProviderAliases      []string        `json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string          `json:"primaryCloudProviderAlias,omitempty"`
	Environments              []uiEnvironment `json:"environments"`
}

type uiEnvironment struct {
	Name              string `json:"name"`
	Type              string `json:"type,omitempty"`
	MCPURL            string `json:"mcpUrl,omitempty"`
	APIURL            string `json:"apiUrl,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	IsActive          bool   `json:"isActive,omitempty"`
	SSHDEnabled       bool   `json:"sshdEnabled,omitempty"`
	AutoStart         *bool  `json:"autoStart,omitempty"`
}

// uiWorkingIssue backs the sidebar hover card's "what is this env working on".
// Available is false when the env worktree isn't reachable from the host
// (remote-agent / runtime envs).
type uiWorkingIssue struct {
	Available   bool   `json:"available"`
	Branch      string `json:"branch,omitempty"`
	IssueNumber int    `json:"issueNumber,omitempty"`
	IssueTitle  string `json:"issueTitle,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// Env-status values carried by the env-status event. The empty string clears
// the state (the env is healthy again, or a fresh open attempt is in flight).
// The two stopped values are separate because their recovery differs: a stopped
// cloud context is started from the titlebar, while a runtime scaled to zero is
// woken by opening the environment.
const (
	envStatusStopped        = "stopped"
	envStatusRuntimeStopped = "runtime-stopped"
	envStatusFailed         = "failed"
)

// envStatusPayload tells the sidebar the real per-env condition behind the
// open dot (tab presence alone is not running-ness — the dot must
// not show green for an env that is actually stopped or whose deploy failed).
// Status is one of "", envStatusStopped, envStatusRuntimeStopped, envStatusFailed.
type envStatusPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Status      string `json:"status"`
}

// uiEnvironmentStopResult is what the Runtime tab's Stop control reports back.
type uiEnvironmentStopResult struct {
	Tenant         string `json:"tenant"`
	Environment    string `json:"environment"`
	Release        string `json:"release"`
	Namespace      string `json:"namespace"`
	AlreadyStopped bool   `json:"alreadyStopped"`
}

type uiSelection struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	Version           string `json:"version,omitempty"`
	RuntimeImage      string `json:"runtimeImage,omitempty"`
	RuntimeCPU        string `json:"runtimeCpu,omitempty"`
	RuntimeMemory     string `json:"runtimeMemory,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	ContainerRegistry string `json:"containerRegistry,omitempty"`
	// ClusterRegistry selects the in-cluster erun-registry (resolved from the
	// env's kube-context) instead of the static ContainerRegistry string; the two
	// are mutually exclusive and ClusterRegistry wins when set.
	ClusterRegistry  bool   `json:"clusterRegistry,omitempty"`
	Type             string `json:"type,omitempty"`
	LocalRepoPath    string `json:"localRepoPath,omitempty"`
	NoGit            bool   `json:"noGit,omitempty"`
	SetDefaultTenant bool   `json:"setDefaultTenant,omitempty"`
	// Components is the explicit one-shot deploy selection from the Runtime tab's
	// "Components to deploy" checklist; empty leaves deploy to resolve the env's
	// saved default. Values are chart directory names (plus the runtime release
	// name) — never the wrapped erun-* dependency names.
	Components []string `json:"components,omitempty"`
}

type uiDiffOptions struct {
	Scope          string `json:"scope,omitempty"`
	SelectedCommit string `json:"selectedCommit,omitempty"`
	// Target selects which repository to diff. "" or "env" diffs the
	// environment's runtime working directory (the historical default).
	// "erun" diffs the contribute-mode clone at $HOME/git/erun inside
	// the environment.
	Target string `json:"target,omitempty"`
}

type uiBuildDetails struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

type uiVersion = eruncommon.RuntimeVersionSuggestion

// uiVersionNotice explains why a runtime-image source produced no version
// suggestions, so the picker shows an actionable line instead of rendering
// nothing. Kind is "auth" (private/unauthorized — the operator must log in) or
// "unreachable" (registry or network failure).
type uiVersionNotice struct {
	Image string `json:"image"`
	Kind  string `json:"kind"`
}

// uiVersionSuggestions is the version picker's read model: the deployable
// version choices plus per-source notices for images that could not be listed.
type uiVersionSuggestions struct {
	Suggestions []uiVersion       `json:"suggestions"`
	Notices     []uiVersionNotice `json:"notices,omitempty"`
}

type uiERunConfig struct {
	DefaultTenant  string                  `json:"defaultTenant"`
	CloudProviders []uiCloudProviderStatus `json:"cloudProviders,omitempty"`
	CloudContexts  []uiCloudContextStatus  `json:"cloudContexts,omitempty"`
}

type uiTenantConfig struct {
	Name                      string                  `json:"name"`
	DefaultEnvironment        string                  `json:"defaultEnvironment"`
	APIURL                    string                  `json:"apiUrl"`
	CloudProviderAliases      []string                `json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string                  `json:"primaryCloudProviderAlias,omitempty"`
	CloudProviders            []uiCloudProviderStatus `json:"cloudProviders,omitempty"`
}

type uiTenantDashboardInput struct {
	Tenant             string `json:"tenant"`
	Environment        string `json:"environment,omitempty"`
	APIURL             string `json:"apiUrl"`
	MCPURL             string `json:"mcpUrl,omitempty"`
	KubernetesContext  string `json:"kubernetesContext,omitempty"`
	CloudProviderAlias string `json:"cloudProviderAlias"`

	// mcpBearer is the per-env MCP edge token for the dashboard's MCP API-log
	// read. Set server-side from the desktop identity, never by the frontend;
	// unexported so the secret never crosses the Wails boundary.
	mcpBearer string
}

type uiTenantDashboard struct {
	Tenant          string                    `json:"tenant"`
	Environment     string                    `json:"environment,omitempty"`
	APIURL          string                    `json:"apiUrl,omitempty"`
	APIError        string                    `json:"apiError,omitempty"`
	APILog          string                    `json:"apiLog,omitempty"`
	APILogError     string                    `json:"apiLogError,omitempty"`
	User            *uiTenantDashboardUser    `json:"user,omitempty"`
	Reviews         []uiTenantDashboardReview `json:"reviews,omitempty"`
	MergeQueue      []uiTenantDashboardReview `json:"mergeQueue,omitempty"`
	Builds          []uiTenantDashboardBuild  `json:"builds,omitempty"`
	AuditEvents     []uiTenantDashboardAudit  `json:"auditEvents,omitempty"`
	AuditLogMessage string                    `json:"auditLogMessage,omitempty"`
}

type uiTenantDashboardUser struct {
	TenantID  string   `json:"tenantId"`
	UserID    string   `json:"userId"`
	Username  string   `json:"username,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	Issuer    string   `json:"issuer,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	CreatedAt string   `json:"createdAt,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
}

type uiTenantDashboardReview struct {
	ReviewID          string `json:"reviewId"`
	TenantID          string `json:"tenantId"`
	Name              string `json:"name"`
	TargetBranch      string `json:"targetBranch"`
	SourceBranch      string `json:"sourceBranch"`
	Status            string `json:"status"`
	LastFailedBuildID string `json:"lastFailedBuildId,omitempty"`
	LastReadyBuildID  string `json:"lastReadyBuildId,omitempty"`
	LastMergedBuildID string `json:"lastMergedBuildId,omitempty"`
	CreatedAt         string `json:"createdAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

type uiTenantDashboardBuild struct {
	BuildID    string `json:"buildId"`
	TenantID   string `json:"tenantId"`
	ReviewID   string `json:"reviewId"`
	ReviewName string `json:"reviewName,omitempty"`
	Successful bool   `json:"successful"`
	CommitID   string `json:"commitId"`
	Version    string `json:"version"`
	CreatedAt  string `json:"createdAt,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}

type uiTenantDashboardAudit struct {
	Type      string `json:"type"`
	Actor     string `json:"actor,omitempty"`
	Action    string `json:"action"`
	CreatedAt string `json:"createdAt,omitempty"`
}

type uiSSHDConfig struct {
	Enabled                    bool   `json:"enabled"`
	LocalPort                  int    `json:"localPort"`
	PublicKeyPath              string `json:"publicKeyPath"`
	WorkspaceSyncEnabled       bool   `json:"workspaceSyncEnabled"`
	WorkspaceSyncLocalPath     string `json:"workspaceSyncLocalPath,omitempty"`
	WorkspaceSyncStatus        string `json:"workspaceSyncStatus,omitempty"`
	WorkspaceSyncStatusMessage string `json:"workspaceSyncStatusMessage,omitempty"`
}

type uiEnvironmentLocalPorts struct {
	RangeStart          int          `json:"rangeStart"`
	RangeEnd            int          `json:"rangeEnd"`
	MCP                 int          `json:"mcp"`
	API                 int          `json:"api"`
	SSH                 int          `json:"ssh"`
	ContributeApp       int          `json:"contributeApp"`
	MCPStatus           uiPortStatus `json:"mcpStatus"`
	APIStatus           uiPortStatus `json:"apiStatus"`
	SSHStatus           uiPortStatus `json:"sshStatus"`
	ContributeAppStatus uiPortStatus `json:"contributeAppStatus"`
}

type uiPortStatus struct {
	Available bool   `json:"available"`
	Status    string `json:"status"`
}

// uiContainerRegistryEntry mirrors eruncommon.ContainerRegistryEntry for the
// desktop registry-list editor. Roles carry the value set build/from/to/deploy
// but ride as plain strings because RegistryRole is a string alias at the Wails
// boundary.
type uiContainerRegistryEntry struct {
	Registry string `json:"registry"`
	// Cluster is set for a context-resolved in-cluster registry (a `cluster:`
	// entry) instead of a static Registry host. It carries a legible Label so the
	// editor renders it as a readable line rather than a blank text box, and its
	// concrete fields so the entry round-trips losslessly on save.
	Cluster *uiContainerRegistryCluster `json:"cluster,omitempty"`
	Roles   []string                    `json:"roles"`
}

// uiContainerRegistryCluster mirrors eruncommon.ClusterRegistry for the desktop
// registry-list editor, plus a Label the frontend shows verbatim.
type uiContainerRegistryCluster struct {
	Service   string `json:"service"`
	Namespace string `json:"namespace"`
	Port      int    `json:"port"`
	Insecure  bool   `json:"insecure"`
	Label     string `json:"label"`
}

type uiEnvironmentConfig struct {
	Name                string                     `json:"name"`
	Type                eruncommon.EnvironmentType `json:"type,omitempty"`
	LocalRepoPath       string                     `json:"localRepoPath,omitempty"`
	RepoPath            string                     `json:"repoPath"`
	KubernetesContext   string                     `json:"kubernetesContext"`
	ContainerRegistries []uiContainerRegistryEntry `json:"containerRegistries"`
	// ContainerRegistriesInherited is true when the shown list is resolved from
	// the project's .erun/config.yaml (a local-agent env with no env-level
	// override) rather than carried on the env config. The editor marks it so the
	// operator sees these are inherited-from-project, not an env override.
	ContainerRegistriesInherited bool                      `json:"containerRegistriesInherited"`
	CloudProviderAlias           string                    `json:"cloudProviderAlias"`
	CloudProviderAliases         []string                  `json:"cloudProviderAliases,omitempty"`
	CloudAliasSlots              []uiEnvironmentCloudAlias `json:"cloudAliasSlots,omitempty"`
	CloudContext                 *uiCloudContextStatus     `json:"cloudContext,omitempty"`
	RuntimeVersion               string                    `json:"runtimeVersion"`
	RuntimePod                   uiRuntimePodConfig        `json:"runtimePod"`
	SSHD                         uiSSHDConfig              `json:"sshd"`
	Idle                         uiIdleConfig              `json:"idle"`
	Claude                       uiClaudeConfig            `json:"claude"`
	ClaudeDefaults               uiClaudeDefaults          `json:"claudeDefaults"`
	AITool                       string                    `json:"aiTool,omitempty"`
	LocalPorts                   uiEnvironmentLocalPorts   `json:"localPorts"`
	AutoStart                    *bool                     `json:"autoStart,omitempty"`
	RemoteHostCredentials        bool                      `json:"remoteHostCredentials"`
	AutoUpgrade                  bool                      `json:"autoUpgrade"`
	UpgradeChannel               string                    `json:"upgradeChannel,omitempty"`
	DisableBuildScript           bool                      `json:"disableBuildScript"`
	// PlatformAccount binds the env's runtime ServiceAccount to cluster-admin so
	// in-pod platform Terraform (the cluster edge) and component installs can
	// manage cluster-scoped resources. See EnvConfig.PlatformAccount.
	PlatformAccount bool `json:"platformAccount"`
	// MountSource opts a runtime env into a mutable source worktree the pod clones
	// at the deployed release ref; RepoURL is the git remote it clones. Runtime
	// envs only, and a no-op without RepoURL. See EnvConfig.MountsRuntimeSource.
	MountSource bool   `json:"mountSource"`
	RepoURL     string `json:"repoURL"`
	// DeployComponents is the per-machine saved deploy selection: the charts
	// `erun deploy` rolls out for this env by default. Empty means no saved
	// selection — deploy falls back to the repo plan, then the runtime chart
	// alone. Editing it raises the pending-redeploy banner, since it changes what
	// a redeploy rolls out.
	DeployComponents []string `json:"deployComponents,omitempty"`
}

type uiClaudeConfig struct {
	UseMantle       *bool    `json:"useMantle,omitempty"`
	UseBedrock      *bool    `json:"useBedrock,omitempty"`
	Models          []string `json:"models,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	Effort          *string  `json:"effort,omitempty"`
	DefaultModel    *string  `json:"defaultModel,omitempty"`
	VerboseDebug    bool     `json:"verboseDebug,omitempty"`
}

type uiClaudeDefaults struct {
	UseMantle       bool     `json:"useMantle"`
	UseBedrock      bool     `json:"useBedrock"`
	Models          []string `json:"models"`
	MaxOutputTokens int      `json:"maxOutputTokens"`
	KnownModels     []string `json:"knownModels"`
	MinTokens       int      `json:"minTokens"`
	MaxTokens       int      `json:"maxTokens"`
	Effort          string   `json:"effort"`
	EffortLevels    []string `json:"effortLevels"`
}

type uiRuntimePodConfig struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type uiRuntimeResourceInput struct {
	KubernetesContext string `json:"kubernetesContext"`
	Tenant            string `json:"tenant,omitempty"`
	Environment       string `json:"environment,omitempty"`
}

// uiRuntimeResourceStatus is a reading of node capacity taken at one instant,
// not a fixed ceiling: allocatable moves as the node's own reservations move,
// and the free figure depends on what every other pod currently holds. Notice
// carries the explanation the bare number cannot — why the figure is capped,
// and what would raise it.
type uiRuntimeResourceStatus struct {
	KubernetesContext string `json:"kubernetesContext"`
	Available         bool   `json:"available"`
	Message           string `json:"message,omitempty"`
	Notice            string `json:"notice,omitempty"`
	Node              string `json:"node,omitempty"`
	// Floored marks a reading where free capacity was clamped up to what this
	// environment already holds, so its maximum equals its current limit. Without
	// it the slider reads as a hard product limit rather than "the node is full".
	Floored bool `json:"floored"`
	// MeasuredUsage reports whether a metrics source answered, so the UI can say
	// whether unlimited containers were counted at their real usage or not
	// counted at all.
	MeasuredUsage bool `json:"measuredUsage"`
	// UnmeasuredContainers counts containers on the chosen node that declare no
	// limits and had no measured usage either — capacity this reading cannot see.
	UnmeasuredContainers int                     `json:"unmeasuredContainers,omitempty"`
	CPU                  uiRuntimeResourceMetric `json:"cpu"`
	Memory               uiRuntimeResourceMetric `json:"memory"`
	Nodes                []uiRuntimeResourceNode `json:"nodes,omitempty"`
}

type uiRuntimeResourceMetric struct {
	Total     float64 `json:"total"`
	Used      float64 `json:"used"`
	Free      float64 `json:"free"`
	Unit      string  `json:"unit"`
	Formatted string  `json:"formatted"`
	// Floored marks this metric's free value as clamped up to the environment's
	// own current limit because the node had nothing left to give.
	Floored bool `json:"floored"`
}

// uiRuntimeActivity is one live reading of what an environment's runtime pod is
// running: its persistent desktop sessions and the processes holding memory.
type uiRuntimeActivity struct {
	Tenant          string                  `json:"tenant"`
	Environment     string                  `json:"environment"`
	Available       bool                    `json:"available"`
	Message         string                  `json:"message,omitempty"`
	SessionsRunning int                     `json:"sessionsRunning"`
	Sessions        []uiRuntimeSession      `json:"sessions,omitempty"`
	Processes       []uiRuntimeProcessGroup `json:"processes,omitempty"`
	MemoryHeld      string                  `json:"memoryHeld,omitempty"`
	MemoryHeldMiB   int64                   `json:"memoryHeldMiB,omitempty"`
}

// uiRuntimeSession is one persistent desktop session as the pod reports it:
// Running is observed (a live program behind the socket), never inferred from
// how recently the session printed something.
type uiRuntimeSession struct {
	ID      string `json:"id"`
	Running bool   `json:"running"`
	Program string `json:"program,omitempty"`
}

// uiRuntimeProcessGroup is a class of resource-holding process the operator can
// recognise and, when Reclaim is set, act on.
type uiRuntimeProcessGroup struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Count        int    `json:"count"`
	Memory       string `json:"memory"`
	MemoryMiB    int64  `json:"memoryMiB"`
	Reclaim      string `json:"reclaim,omitempty"`
	ReclaimLabel string `json:"reclaimLabel,omitempty"`
}

type uiRuntimeReclaimInput struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Action      string `json:"action"`
}

type uiRuntimeReclaimResult struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

// uiClusterRegistryStatus reports whether the selected Kubernetes context has an
// in-cluster erun-registry deployed, so the new-environment dialog can default to
// a resolvable cluster: registry entry instead of a hardcoded host.
type uiClusterRegistryStatus struct {
	KubernetesContext string `json:"kubernetesContext"`
	Deployed          bool   `json:"deployed"`
	Service           string `json:"service,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
	Port              int    `json:"port,omitempty"`
	Insecure          bool   `json:"insecure"`
	Message           string `json:"message,omitempty"`
}

type uiRuntimeResourceNode struct {
	Name   string                  `json:"name"`
	CPU    uiRuntimeResourceMetric `json:"cpu"`
	Memory uiRuntimeResourceMetric `json:"memory"`
}

// Health-check statuses. "ok" is a passing check, "error" a blocking problem
// the operator must fix before the env can run, "unknown" a check that could
// not be evaluated (e.g. no kube-context to query).
const (
	healthCheckStatusOK      = "ok"
	healthCheckStatusError   = "error"
	healthCheckStatusUnknown = "unknown"
)

// Health-check fix affordances the frontend maps to a recovery action.
const (
	healthCheckFixNone        = ""
	healthCheckFixDeploy      = "deploy"
	healthCheckFixSetRegistry = "set-registry"
)

// Stable health-check ids so the frontend can key on the check, not its prose.
const (
	healthCheckIDRegistry = "container-registry"
	healthCheckIDRuntime  = "runtime-deployed"
)

// uiEnvironmentHealthCheck is one out-of-pod diagnostic result. Title is the
// user-facing check name; Detail explains the current outcome; Fix names the
// recovery action the frontend renders (empty when the check passed).
type uiEnvironmentHealthCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// uiEnvironmentHealth is the result of a "Check environment" run: the checks it
// evaluated and whether every one passed.
type uiEnvironmentHealth struct {
	Tenant      string                     `json:"tenant"`
	Environment string                     `json:"environment"`
	Healthy     bool                       `json:"healthy"`
	Checks      []uiEnvironmentHealthCheck `json:"checks"`
}

type uiIdleConfig struct {
	Timeout          string `json:"timeout"`
	WorkingHours     string `json:"workingHours"`
	IdleTrafficBytes int64  `json:"idleTrafficBytes"`
}

type uiCloudProviderStatus struct {
	Alias         string `json:"alias"`
	Provider      string `json:"provider"`
	Username      string `json:"username,omitempty"`
	AccountID     string `json:"accountId,omitempty"`
	Profile       string `json:"profile,omitempty"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty"`
	Status        string `json:"status"`
	Message       string `json:"message,omitempty"`
}

type uiCloudProviderBearerToken struct {
	Alias    string                `json:"alias"`
	Issuer   string                `json:"issuer,omitempty"`
	Token    string                `json:"token"`
	Provider uiCloudProviderStatus `json:"provider"`
}

type uiCloudContextStatus struct {
	Name                string `json:"name"`
	Provider            string `json:"provider"`
	CloudProviderAlias  string `json:"cloudProviderAlias"`
	Region              string `json:"region"`
	InstanceID          string `json:"instanceId,omitempty"`
	PublicIP            string `json:"publicIp,omitempty"`
	InstanceType        string `json:"instanceType"`
	DiskType            string `json:"diskType"`
	DiskSizeGB          int    `json:"diskSizeGb"`
	KubernetesContext   string `json:"kubernetesContext"`
	Status              string `json:"status"`
	Message             string `json:"message,omitempty"`
	StopProtection      bool   `json:"stopProtection,omitempty"`
	StopProtectionKnown bool   `json:"stopProtectionKnown,omitempty"`
}

type uiAWSCloudAliasInput struct {
	Alias         string `json:"alias,omitempty"`
	Username      string `json:"username,omitempty"`
	AccountID     string `json:"accountId,omitempty"`
	Profile       string `json:"profile,omitempty"`
	SSORegion     string `json:"ssoRegion,omitempty"`
	SSOStartURL   string `json:"ssoStartUrl,omitempty"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty"`
}

// uiEnvironmentCloudAlias is one provider-type slot in the env's cloud-alias
// view. The frontend renders one selector per slot so an env can attach an AWS
// alias AND a Cloudflare alias at once.
type uiEnvironmentCloudAlias struct {
	Provider string   `json:"provider"`
	Alias    string   `json:"alias"`
	Options  []string `json:"options"`
}

type uiCloudContextInitInput struct {
	Name               string `json:"name,omitempty"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType"`
	DiskType           string `json:"diskType"`
	DiskSizeGB         int    `json:"diskSizeGb"`
}

type startSessionResult struct {
	SessionID int         `json:"sessionId"`
	Selection uiSelection `json:"selection"`
	Slot      int         `json:"slot,omitempty"`
	Kind      string      `json:"kind,omitempty"`
	// Orchestrated is true when the call started a background command
	// orchestration (e.g. an agent-env deploy composing build -> push ->
	// deploy) instead of a foreground PTY session. There is no Local tab to
	// activate; progress and completion surface through the activity queue.
	Orchestrated bool `json:"orchestrated,omitempty"`
}

type deleteEnvironmentResult struct {
	Tenant                string `json:"tenant"`
	Environment           string `json:"environment"`
	Namespace             string `json:"namespace,omitempty"`
	KubernetesContext     string `json:"kubernetesContext,omitempty"`
	NamespaceDeleteError  string `json:"namespaceDeleteError,omitempty"`
	CloudContextStopError string `json:"cloudContextStopError,omitempty"`
}

type terminalOutputPayload struct {
	SessionID int    `json:"sessionId"`
	Data      string `json:"data"`
}

type terminalExitPayload struct {
	SessionID int    `json:"sessionId"`
	Reason    string `json:"reason,omitempty"`
}

// aiActivityPayload carries the debounced AI-session "busy" signal the sidebar
// uses to spin env rows whose AI tab is actively producing output. Busy flips
// true after ~5 s of sustained output and back to false after ~3 s of silence.
type aiActivityPayload struct {
	SessionID   int    `json:"sessionId"`
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Busy        bool   `json:"busy"`
}

type appStatusPayload struct {
	Message string `json:"message"`
	Busy    bool   `json:"busy"`
}

// appNotificationPayload carries a transient toast-style notification.
// Unlike appStatusPayload, the frontend routes this through the auto-
// dismissing notification slot, so one-shot info/success events cannot
// go stale and outlive the state they describe. Kind matches the
// frontend's AppNotification kinds: success | warning | error | info.
type appNotificationPayload struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	// Tenant/Environment/Source tag a notification so it can be cleared later
	// by the state it describes. The runtime-unreachable warning
	// carries the env it targets and a stable source, so the deploy lifecycle
	// (a deploy starting for that env, or the runtime becoming reachable) can
	// dismiss it without touching an unrelated toast.
	Tenant      string `json:"tenant,omitempty"`
	Environment string `json:"environment,omitempty"`
	Source      string `json:"source,omitempty"`
}

// appNotificationClearPayload dismisses an env-tagged notification. The frontend
// clears the current notification only when its source/tenant/environment all
// match.
type appNotificationClearPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Source      string `json:"source"`
}

type pastedFilePayload struct {
	Data     string `json:"data"`
	MIMEType string `json:"mimeType,omitempty"`
	Name     string `json:"name,omitempty"`
}

type pastedFileResult struct {
	Path string `json:"path"`
}

type uiIdleStatus struct {
	TimeoutSeconds      int64          `json:"timeoutSeconds"`
	SecondsUntilStop    int64          `json:"secondsUntilStop"`
	StopEligible        bool           `json:"stopEligible"`
	OutsideWorkingHours bool           `json:"outsideWorkingHours"`
	ManagedCloud        bool           `json:"managedCloud"`
	StopBlockedReason   string         `json:"stopBlockedReason,omitempty"`
	StopError           string         `json:"stopError,omitempty"`
	CloudContextName    string         `json:"cloudContextName,omitempty"`
	CloudContextStatus  string         `json:"cloudContextStatus,omitempty"`
	CloudContextLabel   string         `json:"cloudContextLabel,omitempty"`
	Markers             []uiIdleMarker `json:"markers,omitempty"`
	// StopPendingSince carries the RFC3339 timestamp at which this env
	// first became StopEligible. When set, the desktop has armed the
	// grace-period warning and the user has SecondsUntilForcedStop
	// seconds to cancel or resume activity before the real
	// ec2:StopInstances fires. Empty when no auto-stop is pending.
	StopPendingSince       string `json:"stopPendingSince,omitempty"`
	SecondsUntilForcedStop int64  `json:"secondsUntilForcedStop,omitempty"`
	GracePeriodSeconds     int64  `json:"gracePeriodSeconds,omitempty"`
}

// uiLastStopEvent describes the most recent automatic stop of a managed cloud
// env, surfaced in the idle tooltip so the user can answer "why did my env
// stop?" without trawling the activity drawer. Source is one of "pod-monitor"
// or "host-manual". ArmedAt is empty for host-manual stops without a prior
// armed grace. Policy is snapshotted at fire time so the row stays
// interpretable after the user later edits the timeout.
type uiLastStopEvent struct {
	StoppedAt        string             `json:"stoppedAt"`
	ArmedAt          string             `json:"armedAt,omitempty"`
	GraceSeconds     int64              `json:"graceSeconds"`
	Source           string             `json:"source,omitempty"`
	Reason           string             `json:"reason"`
	CloudContextName string             `json:"cloudContextName,omitempty"`
	Policy           *uiIdlePolicy      `json:"policy,omitempty"`
	Markers          []uiLastStopMarker `json:"markers,omitempty"`
}

// uiIdlePolicy is the History-tab-facing snapshot of the resolved idle policy.
// It renders TimeoutSeconds rather than a Go duration so the frontend never has
// to parse "10m0s".
type uiIdlePolicy struct {
	TimeoutSeconds   int64  `json:"timeoutSeconds"`
	WorkingHours     string `json:"workingHours,omitempty"`
	Timezone         string `json:"timezone,omitempty"`
	IdleTrafficBytes int64  `json:"idleTrafficBytes,omitempty"`
}

// uiLastStopMarker records the per-marker idle/active state captured
// at the moment the auto-stop fired so the user can see which
// activity sources were quiet.
type uiLastStopMarker struct {
	Name           string `json:"name"`
	Idle           bool   `json:"idle"`
	Reason         string `json:"reason,omitempty"`
	SecondsIdleFor int64  `json:"secondsIdleFor,omitempty"`
}

type uiIdleMarker struct {
	Name             string               `json:"name"`
	Idle             bool                 `json:"idle"`
	Reason           string               `json:"reason,omitempty"`
	SecondsRemaining int64                `json:"secondsRemaining,omitempty"`
	Clients          []uiIdleMarkerClient `json:"clients,omitempty"`
}

// uiIdleMarkerClient is the desktop's per-IP detail row for a marker. Only the
// SSH-proxy populates it; other activity kinds leave Clients nil.
type uiIdleMarkerClient struct {
	Address    string `json:"address"`
	Bytes      int64  `json:"bytes,omitempty"`
	SecondsAgo int64  `json:"secondsAgo,omitempty"`
}
