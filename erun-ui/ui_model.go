package main

import (
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

type uiState struct {
	Tenants                  []uiTenant              `json:"tenants"`
	Selected                 *uimodel.Selection      `json:"selected,omitempty"`
	Message                  string                  `json:"message,omitempty"`
	Build                    uiBuildDetails          `json:"build"`
	VersionSuggestions       []uiVersion             `json:"versionSuggestions,omitempty"`
	VersionSuggestionNotices []uiVersionNotice       `json:"versionSuggestionNotices,omitempty"`
	CloudProviders           []uiCloudProviderStatus `json:"cloudProviders,omitempty"`
	// ConfigUnreadable distinguishes a failed config read from no environments.
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
	// These mirror lifecycle.ListEnvironmentResult's fields of the same name.
	RuntimeVersionLine       *model.RuntimeVersionLine             `json:"runtimeVersionLine,omitempty"`
	ErunVersion              *model.ErunVersion                    `json:"erunVersion,omitempty"`
	RuntimeImageLineMismatch *model.RuntimeImageLineMismatchResult `json:"runtimeImageLineMismatch,omitempty"`
	KubernetesContext        string                                `json:"kubernetesContext,omitempty"`
	IsActive                 bool                                  `json:"isActive,omitempty"`
	SSHDEnabled              bool                                  `json:"sshdEnabled,omitempty"`
	AutoStart                *bool                                 `json:"autoStart,omitempty"`
	// Activity seeds a frontend store reset; the poller only emits on transitions.
	// nil means not yet observed.
	Activity *uiEnvironmentActivitySnapshot `json:"activity,omitempty"`
	// Usage is the usage poller's last reading; nil means not yet observed.
	Usage *uiEnvironmentUsageSnapshot `json:"usage,omitempty"`
	// Node nil means erun manages no node for this env, never a failed read.
	Node *uiEnvironmentNodeSnapshot `json:"node,omitempty"`
}

// uiEnvironmentNodeSnapshot describes the node, kept apart from environment
// state: a stopped node and a stopped environment have different remedies.
type uiEnvironmentNodeSnapshot struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// Status "unknown" (stale) and "" (unobserved) must never render as stopped.
	Status string `json:"status"`
}

// envNodePayload is emitted on transitions; a nil Node is still published.
type envNodePayload struct {
	Tenant      string                     `json:"tenant"`
	Environment string                     `json:"environment"`
	Node        *uiEnvironmentNodeSnapshot `json:"node,omitempty"`
}

// uiEnvironmentActivitySnapshot mirrors envActivityPayload without the identity.
type uiEnvironmentActivitySnapshot struct {
	Reachable bool `json:"reachable"`
	Observed  bool `json:"observed"`
	Outage    bool `json:"outage"`
	// CheckFailed is a kubectl-exec probe that did not answer, distinct from Outage.
	CheckFailed bool   `json:"checkFailed,omitempty"`
	Busy        bool   `json:"busy"`
	Detail      string `json:"detail,omitempty"`
}

// uiEnvironmentUsageSnapshot lets the renderer mark a reading stale without
// knowing the sweep interval.
type uiEnvironmentUsageSnapshot struct {
	Usage             uiRuntimeUsage `json:"usage"`
	ObservedAtUnix    int64          `json:"observedAtUnix"`
	StaleAfterSeconds int64          `json:"staleAfterSeconds"`
}

// uiWorkingIssue: Available is false when the worktree is not on this host.
type uiWorkingIssue struct {
	Available   bool   `json:"available"`
	Branch      string `json:"branch,omitempty"`
	IssueNumber int    `json:"issueNumber,omitempty"`
	IssueTitle  string `json:"issueTitle,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// envActivityPayload is replaced every poll, unlike the sticky uimodel.EnvStatusPayload.
type envActivityPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Reachable   bool   `json:"reachable"`
	// Observed separates "answered, no work" from "no answer".
	Observed bool `json:"observed"`
	// Outage diagnoses a dropped forward that reconnecting did not fix, so the row
	// does not look merely quiet.
	Outage      bool   `json:"outage"`
	CheckFailed bool   `json:"checkFailed,omitempty"`
	Busy        bool   `json:"busy"`
	Detail      string `json:"detail,omitempty"`
}

// envUsagePayload is published on the sweep cadence, not per hover.
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
	// RemainingComponents names components the stop deliberately left running.
	RemainingComponents []string `json:"remainingComponents,omitempty"`
}

// uiRuntimeRunState lets Stop be offered only when the Deployment wants pods.
// On a failed read Present is false and Message is set; that is not "not deployed".
type uiRuntimeRunState struct {
	Tenant          string `json:"tenant"`
	Environment     string `json:"environment"`
	Present         bool   `json:"present"`
	DesiredReplicas int    `json:"desiredReplicas"`
	ReadyReplicas   int    `json:"readyReplicas"`
	// Stopped is distinct from pods that exist but are not ready.
	Stopped bool `json:"stopped"`
	// RemainingComponents names what a stop would leave running.
	RemainingComponents []string `json:"remainingComponents,omitempty"`
	Message             string   `json:"message,omitempty"`
}

type uiDiffOptions struct {
	Scope          string `json:"scope,omitempty"`
	SelectedCommit string `json:"selectedCommit,omitempty"`
	// Target "" or "env" diffs the runtime workdir; "erun" the contribute clone.
	Target string `json:"target,omitempty"`
}

type uiBuildDetails struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

type uiVersion = registry.RuntimeVersionSuggestion

// uiVersionNotice explains empty suggestions; Kind is "auth" or "unreachable".
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
	// OpenRouter is one root-level catalog every environment selects from.
	OpenRouter *uiOpenRouterConfig `json:"openRouter,omitempty"`
	// OpenRouterDefaults pre-fills the catalog from this machine's Claude Code; it
	// is not persisted.
	OpenRouterDefaults *uiHostGatewayDefaults `json:"openRouterDefaults,omitempty"`
}

// uiOpenRouterModel carries the context window, which a gateway id lacks.
type uiOpenRouterModel struct {
	ID      string `json:"id"`
	Context int    `json:"context,omitempty"`
	// RequiresReasoningEcho must round-trip: the editor writes the whole catalog
	// back, so a dropped field would be erased.
	RequiresReasoningEcho bool `json:"requiresReasoningEcho,omitempty"`
}

// uiOpenRouterConfig references the credential as a Secret, never a value.
type uiOpenRouterConfig struct {
	BaseURL string `json:"baseUrl,omitempty"`
	// AuthTokenRef is a secret store ref; the token never enters config.
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

// uiExposureList: Configured false means exposure cannot apply, distinct from
// Restricted and from an empty list. NotConfiguredReason separates a host env
// from a project missing its platform: block.
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

// uiExposedService: TLSReady matters only for https.
type uiExposedService struct {
	Service           string `json:"service"`
	Hostname          string `json:"hostname"`
	Scheme            string `json:"scheme"`
	BackendService    string `json:"backendService,omitempty"`
	TLSReady          bool   `json:"tlsReady,omitempty"`
	TLSNotReadyReason string `json:"tlsNotReadyReason,omitempty"`
}

// uiEnvironmentServiceList mirrors uiExposureList's three states.
type uiEnvironmentServiceList struct {
	Configured          bool                   `json:"configured"`
	Restricted          bool                   `json:"restricted"`
	Error               string                 `json:"error,omitempty"`
	Services            []uiEnvironmentService `json:"services"`
	NotConfiguredReason string                 `json:"notConfiguredReason,omitempty"`
}

// uiEnvironmentService lists Ports so the picker can offer one.
type uiEnvironmentService struct {
	Name     string                     `json:"name"`
	Type     string                     `json:"type,omitempty"`
	Ports    []uiEnvironmentServicePort `json:"ports,omitempty"`
	Hostname string                     `json:"hostname,omitempty"`
	Scheme   string                     `json:"scheme,omitempty"`
	// ExposedAs is the hostname label, not necessarily the Service name.
	ExposedAs string `json:"exposedAs,omitempty"`
}

type uiEnvironmentServicePort struct {
	Name string `json:"name,omitempty"`
	Port int    `json:"port"`
}

// uiExposeServiceInput: empty BackendService derives <tenant>-<service>.
type uiExposeServiceInput struct {
	Service        string `json:"service"`
	BackendService string `json:"backendService,omitempty"`
	TargetIP       string `json:"targetIP"`
	Port           int    `json:"port,omitempty"`
}

// uiUnexposeResult names the DNS record removed.
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

// uiEnvironmentCloudAlias is one provider-type slot, so AWS and Cloudflare
// aliases can coexist.
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
	// Deliberate marks an exit the desktop caused, whose Reason describes the kill
	// rather than a failure.
	Deliberate bool `json:"deliberate,omitempty"`
}

// aiActivityPayload drives the sidebar spinner. The tool's turn report wins;
// the output-volume debounce applies only to sessions that never reported.
type aiActivityPayload struct {
	SessionID   int    `json:"sessionId"`
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Busy        bool   `json:"busy"`
}

// orchestratorShellActivityPayload is emitted every tick so a missed event
// self-heals.
type orchestratorShellActivityPayload struct {
	SessionID     int    `json:"sessionId"`
	Running       bool   `json:"running"`
	Command       string `json:"command,omitempty"`
	StartedAtUnix int64  `json:"startedAtUnix,omitempty"`
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
	// FromPod false means a host-side reconstruction, not a live observation.
	FromPod            bool           `json:"fromPod"`
	StopBlockedReason  string         `json:"stopBlockedReason,omitempty"`
	StopError          string         `json:"stopError,omitempty"`
	CloudContextName   string         `json:"cloudContextName,omitempty"`
	CloudContextStatus string         `json:"cloudContextStatus,omitempty"`
	CloudContextLabel  string         `json:"cloudContextLabel,omitempty"`
	Markers            []uiIdleMarker `json:"markers,omitempty"`
	// Leases are job claims; the desktop's own sessions never take one.
	Leases []uimodel.EnvironmentLease `json:"leases,omitempty"`
	// StopPendingSince is when the env became StopEligible; empty when no auto-stop
	// is pending.
	StopPendingSince       string `json:"stopPendingSince,omitempty"`
	SecondsUntilForcedStop int64  `json:"secondsUntilForcedStop,omitempty"`
	GracePeriodSeconds     int64  `json:"gracePeriodSeconds,omitempty"`
}

// uiLastStopEvent explains the latest automatic stop. Source is "pod-monitor" or
// "host-manual"; Policy is snapshotted at fire time.
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

// uiIdlePolicy uses seconds so the frontend never parses Go durations.
type uiIdlePolicy struct {
	TimeoutSeconds   int64  `json:"timeoutSeconds"`
	WorkingHours     string `json:"workingHours,omitempty"`
	Timezone         string `json:"timezone,omitempty"`
	IdleTrafficBytes int64  `json:"idleTrafficBytes,omitempty"`
}

// uiLastStopMarker is a marker's state when the auto-stop fired.
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

// uiIdleMarkerClient is populated only by the SSH proxy.
type uiIdleMarkerClient struct {
	Address    string `json:"address"`
	Bytes      int64  `json:"bytes,omitempty"`
	SecondsAgo int64  `json:"secondsAgo,omitempty"`
}
