package main

import (
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

type uiState struct {
	Tenants                  []uiTenant              `json:"tenants"`
	Selected                 *uiSelection            `json:"selected,omitempty"`
	Message                  string                  `json:"message,omitempty"`
	Build                    uiBuildDetails          `json:"build"`
	VersionSuggestions       []uiVersion             `json:"versionSuggestions,omitempty"`
	VersionSuggestionNotices []uiVersionNotice       `json:"versionSuggestionNotices,omitempty"`
	CloudProviders           []uiCloudProviderStatus `json:"cloudProviders,omitempty"`
	// ConfigUnreadable is set when one or more config files could not be read
	// (e.g. a torn write) even after erun-common's own retries, so Tenants is
	// empty for a reason other than "no environments configured". The
	// sidebar must render this distinctly from a genuine empty state rather
	// than showing "Create your first environment" for a read failure.
	ConfigUnreadable bool `json:"configUnreadable,omitempty"`
}

type uiTenant struct {
	Name                      string          `json:"name"`
	DefaultEnvironment        string          `json:"defaultEnvironment,omitempty"`
	CloudProviderAliases      []string        `json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string          `json:"primaryCloudProviderAlias,omitempty"`
	Environments              []uiEnvironment `json:"environments"`
}

type uiEnvironment struct {
	Name           string `json:"name"`
	Type           string `json:"type,omitempty"`
	MCPURL         string `json:"mcpUrl,omitempty"`
	APIURL         string `json:"apiUrl,omitempty"`
	RuntimeVersion string `json:"runtimeVersion,omitempty"`
	// RuntimeVersionLine, ErunVersion, and RuntimeImageLineMismatch mirror
	// eruncommon.ListEnvironmentResult's own fields of the same name -- see
	// erun-common/runtime_version_line.go and erun-common/list.go. The
	// environment hover card is the only consumer today.
	RuntimeVersionLine       *deploy.RuntimeVersionLine                `json:"runtimeVersionLine,omitempty"`
	ErunVersion              *deploy.ErunVersion                       `json:"erunVersion,omitempty"`
	RuntimeImageLineMismatch *lifecycle.RuntimeImageLineMismatchResult `json:"runtimeImageLineMismatch,omitempty"`
	KubernetesContext        string                                    `json:"kubernetesContext,omitempty"`
	IsActive                 bool                                      `json:"isActive,omitempty"`
	SSHDEnabled              bool                                      `json:"sshdEnabled,omitempty"`
	AutoStart                *bool                                     `json:"autoStart,omitempty"`
	// Activity is the environment-activity poller's last observation for this
	// env, if any. The poller only emits a Wails event on a transition, so a
	// Redux store that resets without the Go process restarting (e.g. the
	// ErrorBoundary "Reload app" button) would otherwise have nothing to seed
	// from and render a still-busy env as idle until its next transition —
	// which for a long agent turn can be tens of minutes away, if it comes
	// before the turn ends at all. nil means the poller has not observed this
	// env yet.
	Activity *uiEnvironmentActivitySnapshot `json:"activity,omitempty"`
	// Usage is the environment-usage poller's last cached reading for this env,
	// if any. Populated on the same "nil means not yet observed" contract as
	// Activity, and for the same reason: a Redux reset that does not restart the
	// Go process would otherwise have no reading to seed a hover card from until
	// the next sweep tick.
	Usage *uiEnvironmentUsageSnapshot `json:"usage,omitempty"`
	// Node is the cloud node this env's cluster runs on, if it has one. nil is
	// the definite answer "this environment is not backed by a node erun
	// manages" — never "the node could not be read", which is Status instead.
	Node *uiEnvironmentNodeSnapshot `json:"node,omitempty"`
}

// uiEnvironmentNodeSnapshot is the machine an environment's cluster runs on,
// as the cloud-context poller last observed it (cloud_context_cache.go). It is
// a fact about the NODE, kept apart from every field above, which are facts
// about the environment: a stopped node and a stopped environment are
// different things with different remedies, and the row that renders them must
// not be able to conflate the two.
type uiEnvironmentNodeSnapshot struct {
	Name string `json:"name"`
	// Label is the operator-facing name for the same node, falling back to Name.
	Label string `json:"label,omitempty"`
	// Status is the poller's cached reading: "running", "pending", "stopped",
	// "unknown" once a known-good reading has gone stale, or "" when the poller
	// has not observed this node yet. The last two both mean "we do not know",
	// and neither may be rendered as stopped.
	Status string `json:"status"`
}

// envNodePayload publishes one environment's node reading, on the same
// transition-only cadence envActivityPayload uses. Node is nil for an
// environment with no node erun manages, which is a state the row renders
// (nothing about a node) rather than a reason to withhold the event.
type envNodePayload struct {
	Tenant      string                     `json:"tenant"`
	Environment string                     `json:"environment"`
	Node        *uiEnvironmentNodeSnapshot `json:"node,omitempty"`
}

// uiEnvironmentActivitySnapshot mirrors envActivityPayload's observation
// fields, minus the tenant/environment identity envActivityPayload carries
// for the event stream — here that identity is already the enclosing
// uiEnvironment.
type uiEnvironmentActivitySnapshot struct {
	Reachable bool `json:"reachable"`
	Observed  bool `json:"observed"`
	Outage    bool `json:"outage"`
	// CheckFailed mirrors environmentActivityState's own field: a real attempt
	// to reach an environment with no local forward (over kubectl exec) that
	// did not come back, as opposed to an environment nobody has asked about at
	// all. Kept apart from Outage for the same reason Outage is kept apart from
	// Reachable — the two failures name different remedies.
	CheckFailed bool   `json:"checkFailed,omitempty"`
	Busy        bool   `json:"busy"`
	Detail      string `json:"detail,omitempty"`
}

// uiEnvironmentUsageSnapshot is the environment-usage poller's last cached
// reading for this env (environment_usage.go), mirroring envUsagePayload minus
// the tenant/environment identity the event stream carries. ObservedAtUnix and
// StaleAfterSeconds let a renderer show the reading's age and mark it stale
// without hardcoding the sweep interval — an unlabelled stale number is worse
// than none.
type uiEnvironmentUsageSnapshot struct {
	Usage             uiRuntimeUsage `json:"usage"`
	ObservedAtUnix    int64          `json:"observedAtUnix"`
	StaleAfterSeconds int64          `json:"staleAfterSeconds"`
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
	Outage bool `json:"outage"`
	// CheckFailed mirrors uiEnvironmentActivitySnapshot's own field; see there
	// for why it is kept apart from both Reachable and Outage.
	CheckFailed bool   `json:"checkFailed,omitempty"`
	Busy        bool   `json:"busy"`
	Detail      string `json:"detail,omitempty"`
}

// envUsagePayload is the usage-sweep counterpart to envActivityPayload: a
// cached reading of what this environment's own runtime pod is using,
// published on the sweep's cadence rather than per-hover (environment_usage.go).
type envUsagePayload struct {
	Tenant            string         `json:"tenant"`
	Environment       string         `json:"environment"`
	Usage             uiRuntimeUsage `json:"usage"`
	ObservedAtUnix    int64          `json:"observedAtUnix"`
	StaleAfterSeconds int64          `json:"staleAfterSeconds"`
}

// uiEnvironmentStopResult is what the Runtime tab's Stop control reports back.
type uiEnvironmentStopResult struct {
	Tenant         string `json:"tenant"`
	Environment    string `json:"environment"`
	Release        string `json:"release"`
	Namespace      string `json:"namespace"`
	AlreadyStopped bool   `json:"alreadyStopped"`
	// RemainingComponents names the platform components the stop deliberately
	// left running (eruncommon.StopEnvironmentResult.RemainingComponents): a
	// stop scales the runtime Deployment alone, so an outcome that does not say
	// which component pods it left holding capacity reads as a stop that did not
	// finish.
	RemainingComponents []string `json:"remainingComponents,omitempty"`
}

// uiRuntimeRunState is the Runtime tab's reading of the environment's runtime
// Deployment, taken before anything is asked of it. Stop is only a real action
// when the Deployment currently wants pods: erun stop reads exactly these
// replica counts to decide its own no-op, so the control that offers the action
// has to consult the same state rather than discover it by being pressed.
//
// Message is the failed-read case. An unreadable cluster leaves Present false,
// which the tab must not render as "not deployed" — see LoadRuntimeRunState.
type uiRuntimeRunState struct {
	Tenant          string `json:"tenant"`
	Environment     string `json:"environment"`
	Present         bool   `json:"present"`
	DesiredReplicas int    `json:"desiredReplicas"`
	ReadyReplicas   int    `json:"readyReplicas"`
	// Stopped is Present && DesiredReplicas == 0, mirroring
	// eruncommon.RuntimeRunState.Stopped — deliberately distinct from "pods
	// exist but are not ready", which is an unhealthy environment.
	Stopped bool `json:"stopped"`
	// RemainingComponents names the platform components a stop of this
	// environment would leave running (eruncommon.StopRemainingComponents), so
	// the control can say what it will not touch before it is pressed rather
	// than leaving the surviving pods to be read afterwards as a failed stop.
	RemainingComponents []string `json:"remainingComponents,omitempty"`
	Message             string   `json:"message,omitempty"`
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

type uiVersion = registry.RuntimeVersionSuggestion

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
	// OpenRouter is the erun-level gateway catalog. It lives in root config
	// because it is one list the operator maintains and every environment
	// selects from it, rather than a per-environment setting.
	OpenRouter *uiOpenRouterConfig `json:"openRouter,omitempty"`
	// OpenRouterDefaults is the gateway this machine's own Claude Code already
	// routes through, so a catalog can be offered pre-filled from it rather than
	// asking the operator to retype what they configured once already. It is a
	// starting point, not stored config: nothing here is persisted until the
	// operator saves the catalog.
	OpenRouterDefaults *uiHostGatewayDefaults `json:"openRouterDefaults,omitempty"`
}

// uiOpenRouterModel is one selectable gateway model id and the context window
// Claude Code must assume for it, since a gateway id carries none of its own.
type uiOpenRouterModel struct {
	ID      string `json:"id"`
	Context int    `json:"context,omitempty"`
	// RequiresReasoningEcho mirrors the catalog's own declaration (see
	// OpenRouterModel in erun-common/openrouter.go). It travels through this
	// type rather than being dropped because the desktop catalog editor writes
	// the whole catalog back on save: a field the conversion did not carry would
	// be removed by the first edit, silently returning the model to the
	// selectable set the declaration exists to keep it out of.
	RequiresReasoningEcho bool `json:"requiresReasoningEcho,omitempty"`
}

// uiOpenRouterConfig is the gateway an environment's Claude Code is routed
// through and the models selectable from it. The credential is a Secret
// reference, never a value, so config.yaml stays safe to back up and share.
type uiOpenRouterConfig struct {
	BaseURL string `json:"baseUrl,omitempty"`
	// AuthTokenRef is the operator secret store ref the gateway credential is
	// saved under. A ref, not a value: the token itself never enters the config,
	// this read model, or a chart value.
	AuthTokenRef string              `json:"authTokenRef,omitempty"`
	DefaultModel string              `json:"defaultModel,omitempty"`
	Models       []uiOpenRouterModel `json:"models,omitempty"`
}

type uiTenantConfig struct {
	Name                      string                  `json:"name"`
	DefaultEnvironment        string                  `json:"defaultEnvironment"`
	APIURL                    string                  `json:"apiUrl"`
	CloudProviderAliases      []string                `json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string                  `json:"primaryCloudProviderAlias,omitempty"`
	CloudProviders            []uiCloudProviderStatus `json:"cloudProviders,omitempty"`
}

// uiExposureList is the Ports tab's read model for an environment's public
// exposures. Configured is false when exposure cannot apply here at all,
// which the tab renders as "not applicable" rather than an empty list —
// distinct from Restricted (the caller cannot see the answer) and from a
// genuinely empty Services list (configured, nothing exposed yet). Error
// carries a listing failure that is neither of those two named cases.
// NotConfiguredReason names which of two distinct causes made Configured
// false, since they call for different copy and different recovery: a host
// environment has no cluster and can never be exposed, while a cluster-backed
// environment whose project simply hasn't declared a platform: block yet is
// the fixable case.
type uiExposureList struct {
	Configured          bool               `json:"configured"`
	Restricted          bool               `json:"restricted"`
	Error               string             `json:"error,omitempty"`
	Services            []uiExposedService `json:"services"`
	NotConfiguredReason string             `json:"notConfiguredReason,omitempty"`
}

// uiExposureNotConfiguredReason enumerates uiExposureList.NotConfiguredReason values.
const (
	uiExposureNotConfiguredHostEnvironment = "host-environment"
	uiExposureNotConfiguredNoPlatformBlock = "no-platform-block"
)

// uiExposedService mirrors eruncommon.ExposedService for the Ports tab list.
// TLSReady/TLSNotReadyReason are meaningful only when Scheme is "https": see
// eruncommon.ExposedService for why Scheme alone cannot say whether the URL
// is actually safe to open yet.
type uiExposedService struct {
	Service           string `json:"service"`
	Hostname          string `json:"hostname"`
	Scheme            string `json:"scheme"`
	BackendService    string `json:"backendService,omitempty"`
	TLSReady          bool   `json:"tlsReady,omitempty"`
	TLSNotReadyReason string `json:"tlsNotReadyReason,omitempty"`
}

// uiEnvironmentServiceList is the Ports tab's read model for what the
// environment is actually running — the list the operator picks from. It
// mirrors uiExposureList's shape (Configured / Restricted / Error) so the tab
// renders the same three states for both reads rather than inventing a second
// vocabulary for the same conditions.
type uiEnvironmentServiceList struct {
	Configured          bool                   `json:"configured"`
	Restricted          bool                   `json:"restricted"`
	Error               string                 `json:"error,omitempty"`
	Services            []uiEnvironmentService `json:"services"`
	NotConfiguredReason string                 `json:"notConfiguredReason,omitempty"`
}

// uiEnvironmentService is one Service in the environment, with the exposure it
// already has. Ports is what makes the picker able to offer a port instead of
// asking the operator to remember one.
type uiEnvironmentService struct {
	Name     string                     `json:"name"`
	Type     string                     `json:"type,omitempty"`
	Ports    []uiEnvironmentServicePort `json:"ports,omitempty"`
	Hostname string                     `json:"hostname,omitempty"`
	Scheme   string                     `json:"scheme,omitempty"`
	// ExposedAs is the logical label in the hostname, which is not necessarily
	// the Service's own name.
	ExposedAs string `json:"exposedAs,omitempty"`
}

type uiEnvironmentServicePort struct {
	Name string `json:"name,omitempty"`
	Port int    `json:"port"`
}

// uiExposeServiceInput is the Ports tab's "Expose a service" form. Service is
// the public label; BackendService is the Service in the namespace to route
// to, which the picker fills in from the operator's choice. Empty keeps the
// <tenant>-<service> derivation for a caller that only knows a label.
type uiExposeServiceInput struct {
	Service        string `json:"service"`
	BackendService string `json:"backendService,omitempty"`
	TargetIP       string `json:"targetIP"`
	Port           int    `json:"port,omitempty"`
}

// uiUnexposeResult confirms which DNS record un-exposing removed, so the
// dialog can name it back to the operator.
type uiUnexposeResult struct {
	WildcardName string `json:"wildcardName"`
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
	// Deliberate reports that the desktop had already ended this session — an
	// env close, a tab close, an AI-session end — when its read ended. Every
	// one of those teardowns kills the PTY, so the reader that wakes on it
	// carries a non-empty Reason ("signal: killed") that describes the kill the
	// operator asked for rather than a failure. The flag travels on the exit
	// itself because the session is the only thing that can answer the
	// question: the set the desktop set out to tear down is not the set it
	// reached, and a session it kills before the frontend has heard of it has
	// no other record anywhere. Absent on an exit nothing deliberate caused.
	Deliberate bool `json:"deliberate,omitempty"`
}

// aiActivityPayload carries the AI-session "busy" signal the sidebar uses to
// spin env rows whose AI tab is working. It has two sources, and the tool's own
// report wins where it exists: the AI tool's turn-boundary events (busy while a
// turn is in flight, released when it reports the turn ended or that it is
// waiting on the operator — see ai_session_status.go), and, only for a session
// that has never reported one, the debounced output-volume fallback (true after
// ~5 s of sustained output, false after ~3 s of silence).
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

// appNotificationPayload carries a classified notification retained in the
// frontend's message-centre history for the session. success/info entries
// auto-dismiss (leaving the history entry behind); warning/error persist
// until acknowledged. Kind matches the frontend's AppNotification kinds:
// success | warning | error | info | debug (debug is never emitted by this
// backend today — the class exists so a future diagnostic emitter has
// somewhere to go that isn't the operator-facing classes).
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
	// OrchestratorID tags a notification about a specific orchestrator so the
	// frontend can render an action that operates on that orchestrator
	// directly (e.g. restarting it), the orchestrator-scoped analogue of
	// Tenant/Environment.
	OrchestratorID string `json:"orchestratorId,omitempty"`
	// Action names a control the frontend can render beside the message that
	// actually performs the remedy the message names, e.g. "deploy" opens the
	// tagged env's deploy dialog. Empty means the message carries no action the
	// app can perform — the operator-facing text is the whole of the recovery.
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
