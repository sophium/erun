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
	// Activity is the environment-activity poller's last observation for this
	// env, if any. The poller only emits a Wails event on a transition, so a
	// Redux store that resets without the Go process restarting (e.g. the
	// ErrorBoundary "Reload app" button) would otherwise have nothing to seed
	// from and render a still-busy env as idle until its next transition —
	// which for a long agent turn can be tens of minutes away, if it comes
	// before the turn ends at all. nil means the poller has not observed this
	// env yet.
	Activity *uiEnvironmentActivitySnapshot `json:"activity,omitempty"`
}

// uiEnvironmentActivitySnapshot mirrors envActivityPayload's observation
// fields, minus the tenant/environment identity envActivityPayload carries
// for the event stream — here that identity is already the enclosing
// uiEnvironment.
type uiEnvironmentActivitySnapshot struct {
	Reachable bool   `json:"reachable"`
	Observed  bool   `json:"observed"`
	Outage    bool   `json:"outage"`
	Busy      bool   `json:"busy"`
	Detail    string `json:"detail,omitempty"`
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

// envActivityPayload is the observed half of the same sidebar state: whether the
// environment's edge answers at all (reachable — true whoever opened it, the
// desktop or a bare `erun open`), and whether the environment reports work in
// flight (busy) and what. Separate from envStatusPayload because the lifetimes
// differ: a status is a sticky condition, this is replaced on every poll.
type envActivityPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Reachable   bool   `json:"reachable"`
	// Observed separates "the environment answered, and said no work" from
	// "nobody got an answer": busy is false in both, and the sidebar acts on
	// the difference when deciding whether a row may stop spinning.
	Observed bool `json:"observed"`
	// Outage is the diagnosis behind an environment that had a forward and no
	// longer has a working one — the local port free after kubectl exited with
	// its pod, or held by something that replies to nothing — once
	// re-establishing it did not help. Without it such a row renders exactly
	// like a quiet environment or an unopened one, which is how one stayed dead
	// for hours. It is deliberately independent of Reachable: the dropped shape
	// is unreachable *and* diagnosed, and only the second of those is news.
	Outage bool   `json:"outage"`
	Busy   bool   `json:"busy"`
	Detail string `json:"detail,omitempty"`
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
	ClusterRegistry bool `json:"clusterRegistry,omitempty"`
	// ErunRegistry selects erun's hosted registry for the tenant. Mutually
	// exclusive with both ContainerRegistry and ClusterRegistry, matching
	// `erun init --erun-registry`.
	ErunRegistry bool `json:"erunRegistry,omitempty"`
	// RuntimeRegistry and ImagePullSecrets carry `erun init`'s
	// --runtime-registry and --image-pull-secret, without which an env created
	// here cannot pull a private runtime image.
	RuntimeRegistry  string   `json:"runtimeRegistry,omitempty"`
	ImagePullSecrets []string `json:"imagePullSecrets,omitempty"`
	Type             string   `json:"type,omitempty"`
	LocalRepoPath    string   `json:"localRepoPath,omitempty"`
	NoGit            bool     `json:"noGit,omitempty"`
	SetDefaultTenant bool     `json:"setDefaultTenant,omitempty"`
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
	// ReviewFilterMine/ReviewFilterWaitingOnMe are the Reviews tab's one-click
	// discovery filters. Both resolve to the signed-in user's own id (already
	// known from this same load's whoami call) rather than asking the
	// frontend to carry a user id it has no other reason to know.
	ReviewFilterMine        bool `json:"reviewFilterMine,omitempty"`
	ReviewFilterWaitingOnMe bool `json:"reviewFilterWaitingOnMe,omitempty"`

	// mcpBearer is the per-env MCP edge token for the dashboard's MCP API-log
	// read. Set server-side from the desktop identity, never by the frontend;
	// unexported so the secret never crosses the Wails boundary.
	mcpBearer string
}

type uiTenantDashboard struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment,omitempty"`
	APIURL      string `json:"apiUrl,omitempty"`
	// APIError is a whole-dashboard failure: the caller's identity could not be
	// read, so no panel can be resolved or gated. A single panel's own failure
	// belongs on that panel, not here.
	APIError    string                    `json:"apiError,omitempty"`
	APILog      string                    `json:"apiLog,omitempty"`
	APILogError string                    `json:"apiLogError,omitempty"`
	User        *uiTenantDashboardUser    `json:"user,omitempty"`
	Reviews     []uiTenantDashboardReview `json:"reviews,omitempty"`
	MergeQueue  []uiTenantDashboardReview `json:"mergeQueue,omitempty"`
	Builds      []uiTenantDashboardBuild  `json:"builds,omitempty"`
	AuditEvents []uiTenantDashboardAudit  `json:"auditEvents,omitempty"`
	Panels      []uiTenantDashboardPanel  `json:"panels,omitempty"`
	// CanCreateReview and CanAdvanceMergeQueue report whether the signed-in user
	// may attempt those writes at all, so the composing actions can be hidden
	// rather than rendered to fail on submit — the same contract CanComment
	// already gives the reply composer.
	CanCreateReview      bool `json:"canCreateReview"`
	CanAdvanceMergeQueue bool `json:"canAdvanceMergeQueue"`
	// MineReviewCount/WaitingOnMeReviewCount are the Reviews tab's filter
	// buttons' own discovery signal: how many reviews match each filter,
	// visible before the caller clicks either one. Unset (rather than 0) when
	// the caller cannot read reviews at all, or has no signed-in user id to
	// filter by.
	MineReviewCount        *int `json:"mineReviewCount,omitempty"`
	WaitingOnMeReviewCount *int `json:"waitingOnMeReviewCount,omitempty"`
}

// uiTenantDashboardPanel is one panel's own outcome. It is what lets the tab
// strip tell "there is nothing here" apart from "you may not look": a panel the
// caller lacks the permission for carries Restricted and is not rendered, while
// one that failed carries Error and does not blank its neighbours.
type uiTenantDashboardPanel struct {
	Tab string `json:"tab"`
	// Restricted names the API read the caller lacks, in canonical form
	// ("GET /v1/audit-events"), so the reason can be shown rather than inferred.
	Restricted string `json:"restricted,omitempty"`
	Error      string `json:"error,omitempty"`
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
	ReviewID     string `json:"reviewId"`
	TenantID     string `json:"tenantId"`
	AuthorUserID string `json:"authorUserId,omitempty"`
	// AuthorUsername is the tenant user directory's display name for
	// AuthorUserID, resolved best-effort (see tenant_dashboard.go's
	// tenantDashboardUsernames). Empty when it could not be resolved, so the
	// frontend falls back to the raw id rather than showing nothing (#1378).
	AuthorUsername string `json:"authorUsername,omitempty"`
	Name           string `json:"name"`
	TargetBranch   string `json:"targetBranch"`
	SourceBranch   string `json:"sourceBranch"`
	Status         string `json:"status"`
	// UnresolvedThreads is the review's "still being discussed" signal at a
	// glance, from the row rather than only inside the detail dialog. Left
	// unset (rather than 0) when it was not computed for this listing (see
	// tenant_dashboard.go's reviewThreadCounts) so a caller can tell "zero
	// unresolved" apart from "not read for this row".
	UnresolvedThreads *int   `json:"unresolvedThreads,omitempty"`
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

type uiReviewDetailInput struct {
	Tenant             string `json:"tenant"`
	APIURL             string `json:"apiUrl"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	ReviewID           string `json:"reviewId"`
}

// uiReviewDetail is the Reviews tab's per-row detail: the review itself, its
// comment threads, its recorded builds, and its position in its target
// branch's merge queue. Each sub-read degrades independently — the same
// restricted-vs-failed-vs-empty distinction the dashboard's own panels make —
// so one forbidden or failing read never blanks the rest of the detail.
type uiReviewDetail struct {
	ReviewID string `json:"reviewId"`
	// APIError is a whole-detail failure: identity could not be read, so no
	// capability set exists to gate the reads below honestly.
	APIError           string                   `json:"apiError,omitempty"`
	Restricted         string                   `json:"restricted,omitempty"`
	Error              string                   `json:"error,omitempty"`
	Review             *uiTenantDashboardReview `json:"review,omitempty"`
	Comments           []uiReviewComment        `json:"comments,omitempty"`
	CommentsRestricted string                   `json:"commentsRestricted,omitempty"`
	CommentsError      string                   `json:"commentsError,omitempty"`
	Builds             []uiTenantDashboardBuild `json:"builds,omitempty"`
	BuildsRestricted   string                   `json:"buildsRestricted,omitempty"`
	BuildsError        string                   `json:"buildsError,omitempty"`
	// QueuePosition is 1-based; 0 means the review is not in its target
	// branch's merge queue right now.
	QueuePosition int `json:"queuePosition,omitempty"`
	// UnresolvedThreads counts root comments still OPEN, valid whenever
	// Comments loaded (CommentsRestricted and CommentsError both empty).
	UnresolvedThreads int `json:"unresolvedThreads,omitempty"`
	// CanComment reports whether the signed-in user may reply at all, so the
	// composer can be hidden rather than rendered to fail on submit.
	CanComment bool `json:"canComment"`
	// CanClose mirrors CanComment for the close action.
	CanClose bool `json:"canClose"`
	// CanResolveComments mirrors CanComment for the resolve/unresolve action,
	// gated on its own write route since resolving a thread and posting to it
	// are different permissions on the platform.
	CanResolveComments bool `json:"canResolveComments"`
}

type uiReviewComment struct {
	CommentID     string `json:"commentId"`
	CreatorUserID string `json:"creatorUserId,omitempty"`
	// CreatorUsername mirrors uiTenantDashboardReview.AuthorUsername: the
	// tenant user directory's display name for CreatorUserID, resolved
	// best-effort, empty when it could not be resolved (#1378).
	CreatorUsername string `json:"creatorUsername,omitempty"`
	Status          string `json:"status"`
	ParentCommentID string `json:"parentCommentId,omitempty"`
	CommitID        string `json:"commitId"`
	FilePath        string `json:"filePath"`
	Line            int    `json:"line"`
	Body            string `json:"body"`
	CreatedAt       string `json:"createdAt,omitempty"`
}

// uiUpdateReviewCommentStatusInput resolves or unresolves a comment thread.
// CommentID must be a thread's root; the frontend never offers the action on
// a reply, so there is no reply-rejection path to surface here.
type uiUpdateReviewCommentStatusInput struct {
	Tenant             string `json:"tenant"`
	APIURL             string `json:"apiUrl"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	ReviewID           string `json:"reviewId"`
	CommentID          string `json:"commentId"`
}

// uiCreateReviewReplyInput replies to an existing comment thread. CommitID,
// FilePath, and Line are copied from the parent comment the frontend already
// holds — a reply must anchor to the same line as the thread it joins — so
// Body is the only field the operator actually authors.
type uiCreateReviewReplyInput struct {
	Tenant             string `json:"tenant"`
	APIURL             string `json:"apiUrl"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	ReviewID           string `json:"reviewId"`
	ParentCommentID    string `json:"parentCommentId"`
	CommitID           string `json:"commitId"`
	FilePath           string `json:"filePath"`
	Line               int    `json:"line"`
	Body               string `json:"body"`
}

// uiCreateReviewInput opens a review on the platform. sourceBranch must
// already be pushed to the remote — see ExecPush — since the review
// references it by name.
type uiCreateReviewInput struct {
	Tenant             string `json:"tenant"`
	APIURL             string `json:"apiUrl"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Name               string `json:"name"`
	TargetBranch       string `json:"targetBranch"`
	SourceBranch       string `json:"sourceBranch"`
}

type uiCloseReviewInput struct {
	Tenant             string `json:"tenant"`
	APIURL             string `json:"apiUrl"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	ReviewID           string `json:"reviewId"`
}

type uiAdvanceMergeQueueInput struct {
	Tenant             string `json:"tenant"`
	APIURL             string `json:"apiUrl"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	TargetBranch       string `json:"targetBranch"`
}

// uiCreateReviewCommentInput starts a new top-level thread anchored to a diff
// line, as opposed to uiCreateReviewReplyInput's reply-in-an-existing-thread.
// Every field but Body is the anchor the operator picked by clicking a line in
// the diff panel, not a value they typed.
type uiCreateReviewCommentInput struct {
	Tenant             string `json:"tenant"`
	APIURL             string `json:"apiUrl"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	ReviewID           string `json:"reviewId"`
	CommitID           string `json:"commitId"`
	FilePath           string `json:"filePath"`
	Line               int    `json:"line"`
	Body               string `json:"body"`
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

// uiExposureList is the Ports tab's read model for an environment's public
// exposures. Configured is false when the project has no platform block at
// all, which the tab renders as "not applicable" rather than an empty list —
// distinct from Restricted (the caller cannot see the answer) and from a
// genuinely empty Services list (configured, nothing exposed yet). Error
// carries a listing failure that is neither of those two named cases.
type uiExposureList struct {
	Configured bool               `json:"configured"`
	Restricted bool               `json:"restricted"`
	Error      string             `json:"error,omitempty"`
	Services   []uiExposedService `json:"services"`
}

// uiExposedService mirrors eruncommon.ExposedService for the Ports tab list.
type uiExposedService struct {
	Service  string `json:"service"`
	Hostname string `json:"hostname"`
	Scheme   string `json:"scheme"`
}

// uiExposeServiceInput is the Ports tab's "Expose a service" form.
type uiExposeServiceInput struct {
	Service  string `json:"service"`
	TargetIP string `json:"targetIP"`
	Port     int    `json:"port,omitempty"`
}

// uiUnexposeResult confirms which DNS record un-exposing removed, so the
// dialog can name it back to the operator.
type uiUnexposeResult struct {
	WildcardName string `json:"wildcardName"`
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

// uiDeployComponents is the Runtime tab's read model for a deploy at a chosen
// version: the charts it would roll out, and which chart the runtime itself would
// be installed from -- the second coordinate the version does not name.
type uiDeployComponents struct {
	Components   []eruncommon.DeployableComponent `json:"components"`
	RuntimeChart uiRuntimeChartPlan               `json:"runtimeChart"`
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
	// RuntimeChart is the chart this env's runtime is installed from, stated as an
	// OCI reference that may carry its own version. Empty means "the chart
	// published with the deployed version", which is right whenever the chart and
	// the runtime image ride one release line. See EnvConfig.RuntimeChart.
	RuntimeChart string `json:"runtimeChart,omitempty"`
	// RuntimeRegistry is where the env resolves erun's OWN artifacts from -- the
	// runtime chart and platform images -- as distinct from the registry this
	// project's images are pushed to. Set it when the deploy registry holds only
	// the project's images, so erun's chart is not there to pull.
	RuntimeRegistry string `json:"runtimeRegistry,omitempty"`
	// ImagePullSecrets names the Kubernetes dockerconfigjson secrets the runtime
	// pod pulls its image with. Without one a private runtime image leaves the
	// pod in ImagePullBackOff, which the app could previously cause and not fix.
	ImagePullSecrets      []string                `json:"imagePullSecrets,omitempty"`
	RuntimePod            uiRuntimePodConfig      `json:"runtimePod"`
	SSHD                  uiSSHDConfig            `json:"sshd"`
	Idle                  uiIdleConfig            `json:"idle"`
	Claude                uiClaudeConfig          `json:"claude"`
	ClaudeDefaults        uiClaudeDefaults        `json:"claudeDefaults"`
	AITool                string                  `json:"aiTool,omitempty"`
	LocalPorts            uiEnvironmentLocalPorts `json:"localPorts"`
	AutoStart             *bool                   `json:"autoStart,omitempty"`
	RemoteHostCredentials bool                    `json:"remoteHostCredentials"`
	AutoUpgrade           bool                    `json:"autoUpgrade"`
	UpgradeChannel        string                  `json:"upgradeChannel,omitempty"`
	DisableBuildScript    bool                    `json:"disableBuildScript"`
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

// uiRuntimeUsage is one live reading of the selected environment's own CPU,
// memory and disk usage against its cgroup limits — as opposed to
// uiRuntimeResourceStatus, which reads the node. Available is the probe's own
// reachability; CPU/Memory/Disk each additionally state their own
// unavailability, since cgroup v1, an unlimited limit, or an unreadable file
// are all normal readings, not probe failures.
type uiRuntimeUsage struct {
	Tenant      string               `json:"tenant"`
	Environment string               `json:"environment"`
	Available   bool                 `json:"available"`
	Message     string               `json:"message,omitempty"`
	CPU         uiRuntimeCPUUsage    `json:"cpu"`
	Memory      uiRuntimeMemoryUsage `json:"memory"`
	Disk        []uiRuntimeDiskUsage `json:"disk,omitempty"`
	Warnings    []string             `json:"warnings,omitempty"`
}

// uiRuntimeCPUUsage carries Available=false with Unavailable set when the
// reader could not measure utilisation at all (no cgroup v2, or cpu.max
// reports no quota) — never a zero utilisation, which would read as "idle"
// rather than "unknown".
type uiRuntimeCPUUsage struct {
	Available          bool    `json:"available"`
	Unavailable        string  `json:"unavailable,omitempty"`
	QuotaCores         float64 `json:"quotaCores,omitempty"`
	Quota              string  `json:"quota,omitempty"`
	UtilizationPercent float64 `json:"utilizationPercent,omitempty"`
	Utilization        string  `json:"utilization,omitempty"`
}

// uiRuntimeMemoryUsage mirrors the reader's own fail-soft shape: Unlimited is
// a real, available reading (the container simply has no ceiling declared),
// distinct from Unavailable (the cgroup file itself could not be read). Both
// suppress the percent-of-limit figure, but only Unavailable is an error
// state the panel should call out.
type uiRuntimeMemoryUsage struct {
	Available      bool    `json:"available"`
	Unavailable    string  `json:"unavailable,omitempty"`
	Unlimited      bool    `json:"unlimited,omitempty"`
	CurrentBytes   int64   `json:"currentBytes,omitempty"`
	Current        string  `json:"current,omitempty"`
	PeakBytes      int64   `json:"peakBytes,omitempty"`
	Peak           string  `json:"peak,omitempty"`
	LimitBytes     int64   `json:"limitBytes,omitempty"`
	Limit          string  `json:"limit,omitempty"`
	PercentOfLimit float64 `json:"percentOfLimit,omitempty"`
	OOMKills       int64   `json:"oomKills"`
}

type uiRuntimeDiskUsage struct {
	Mount       string  `json:"mount"`
	Available   bool    `json:"available"`
	Unavailable string  `json:"unavailable,omitempty"`
	TotalBytes  int64   `json:"totalBytes,omitempty"`
	Total       string  `json:"total,omitempty"`
	UsedBytes   int64   `json:"usedBytes,omitempty"`
	Used        string  `json:"used,omitempty"`
	PercentUsed float64 `json:"percentUsed,omitempty"`
	Percent     string  `json:"percent,omitempty"`
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
	// Occupancy lists the leases already holding the environment when an
	// unconfirmed AI session start found it occupied: SessionID is 0 and no
	// session was started. The caller shows who is already here and, if the
	// user wants a second agent anyway, retries with confirmed=true.
	Occupancy []uiEnvironmentLease `json:"occupancy,omitempty"`
}

// uiEnvironmentLease is one activity lease surfaced from
// eruncommon.EnvironmentActivityLease so the desktop can name the job already
// working in an environment. PID and lease ID are implementation detail, not
// shown; SecondsHeld is precomputed so the renderer never redoes the time math.
type uiEnvironmentLease struct {
	Name        string `json:"name"`
	SecondsHeld int64  `json:"secondsHeld,omitempty"`
	// JobID is set only when the lease is held by a job, so the surface that
	// names the occupancy can also act on it. Empty for every other holder,
	// which is what keeps a non-job lease from rendering a cancel that cannot
	// work.
	JobID string `json:"jobId,omitempty"`
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

// orchestratorShellActivityPayload carries whether an orchestrator's
// background shell is running, its command and when it started, so the
// sidebar can spin and show elapsed time for a shell the orchestrator's own
// turn may already have gone idle around. Emitted every heartbeat tick, busy
// or not, the same re-emit-regardless-of-change treatment aiActivityPayload
// was given earlier and for the same reason: a snapshot field
// (orchestratorInfo.ShellRunning) carries the same signal so a missed or
// mistimed event self-heals within one tick instead of staying wrong until
// the state itself next changes.
type orchestratorShellActivityPayload struct {
	SessionID     int    `json:"sessionId"`
	Running       bool   `json:"running"`
	Command       string `json:"command,omitempty"`
	StartedAtUnix int64  `json:"startedAtUnix,omitempty"`
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
	// Action names a control the frontend can render beside the message that
	// actually performs the remedy the message names, e.g. "deploy" opens the
	// tagged env's deploy dialog. Empty means the message carries no action the
	// app can perform — the operator-facing text is the whole of the recovery
	// (#1390).
	Action string `json:"action,omitempty"`
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
	TimeoutSeconds      int64 `json:"timeoutSeconds"`
	SecondsUntilStop    int64 `json:"secondsUntilStop"`
	StopEligible        bool  `json:"stopEligible"`
	OutsideWorkingHours bool  `json:"outsideWorkingHours"`
	ManagedCloud        bool  `json:"managedCloud"`
	// FromPod is true only when this reading came from the pod's own idle
	// monitor over MCP. False means it was assembled on the host because the
	// pod could not be reached — the same moment the sidebar may be showing
	// this environment as unreachable — so the countdown it carries is a
	// best-effort local reconstruction, not a live observation.
	FromPod            bool           `json:"fromPod"`
	StopBlockedReason  string         `json:"stopBlockedReason,omitempty"`
	StopError          string         `json:"stopError,omitempty"`
	CloudContextName   string         `json:"cloudContextName,omitempty"`
	CloudContextStatus string         `json:"cloudContextStatus,omitempty"`
	CloudContextLabel  string         `json:"cloudContextLabel,omitempty"`
	Markers            []uiIdleMarker `json:"markers,omitempty"`
	// Leases are the work claims currently holding the environment (an
	// orchestrator or CLI job running via job_start/AttachEnvironmentJob) — not
	// this desktop's own interactive AI/ERun/Local sessions, which never take
	// one. A non-empty list is a coexisting agent the AI tab does not manage.
	Leases []uiEnvironmentLease `json:"leases,omitempty"`
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
