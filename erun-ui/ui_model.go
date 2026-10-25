package main

import (
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-ui/internal/runtimeview"
	settingssvc "github.com/sophium/erun/erun-ui/internal/settings"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

type uiState struct {
	Tenants                  []uiTenant                        `json:"tenants"`
	Selected                 *uimodel.Selection                `json:"selected,omitempty"`
	Message                  string                            `json:"message,omitempty"`
	Build                    uiBuildDetails                    `json:"build"`
	VersionSuggestions       []uiVersion                       `json:"versionSuggestions,omitempty"`
	VersionSuggestionNotices []uiVersionNotice                 `json:"versionSuggestionNotices,omitempty"`
	CloudProviders           []settingssvc.CloudProviderStatus `json:"cloudProviders,omitempty"`
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
	Activity *runtimeview.ActivitySnapshot `json:"activity,omitempty"`
	// Usage is the usage poller's last reading; nil means not yet observed.
	Usage *runtimeview.UsageSnapshot `json:"usage,omitempty"`
	// Node nil means erun manages no node for this env, never a failed read.
	Node *runtimeview.NodeSnapshot `json:"node,omitempty"`
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
