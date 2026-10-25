package terminal

import "github.com/sophium/erun/erun-ui/internal/uimodel"

type DeleteEnvironmentResult struct {
	Tenant                string `json:"tenant"`
	Environment           string `json:"environment"`
	Namespace             string `json:"namespace,omitempty"`
	KubernetesContext     string `json:"kubernetesContext,omitempty"`
	NamespaceDeleteError  string `json:"namespaceDeleteError,omitempty"`
	CloudContextStopError string `json:"cloudContextStopError,omitempty"`
}

type terminalExitPayload struct {
	SessionID int    `json:"sessionId"`
	Reason    string `json:"reason,omitempty"`
	// Deliberate marks an exit the desktop caused, whose Reason describes the kill
	// rather than a failure.
	Deliberate bool `json:"deliberate,omitempty"`
}

// AIActivityPayload drives the sidebar spinner. The tool's turn report wins;
// the output-volume debounce applies only to sessions that never reported.
type AIActivityPayload struct {
	SessionID   int    `json:"sessionId"`
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Busy        bool   `json:"busy"`
}

type PastedFilePayload struct {
	Data     string `json:"data"`
	MIMEType string `json:"mimeType,omitempty"`
	Name     string `json:"name,omitempty"`
}

type PastedFileResult struct {
	Path string `json:"path"`
}

type IdleStatus struct {
	TimeoutSeconds      int64 `json:"timeoutSeconds"`
	SecondsUntilStop    int64 `json:"secondsUntilStop"`
	StopEligible        bool  `json:"stopEligible"`
	OutsideWorkingHours bool  `json:"outsideWorkingHours"`
	ManagedCloud        bool  `json:"managedCloud"`
	// FromPod false means a host-side reconstruction, not a live observation.
	FromPod            bool         `json:"fromPod"`
	StopBlockedReason  string       `json:"stopBlockedReason,omitempty"`
	StopError          string       `json:"stopError,omitempty"`
	CloudContextName   string       `json:"cloudContextName,omitempty"`
	CloudContextStatus string       `json:"cloudContextStatus,omitempty"`
	CloudContextLabel  string       `json:"cloudContextLabel,omitempty"`
	Markers            []IdleMarker `json:"markers,omitempty"`
	// Leases are job claims; the desktop's own sessions never take one.
	Leases []uimodel.EnvironmentLease `json:"leases,omitempty"`
	// StopPendingSince is when the env became StopEligible; empty when no auto-stop
	// is pending.
	StopPendingSince       string `json:"stopPendingSince,omitempty"`
	SecondsUntilForcedStop int64  `json:"secondsUntilForcedStop,omitempty"`
	GracePeriodSeconds     int64  `json:"gracePeriodSeconds,omitempty"`
}

// LastStopEvent explains the latest automatic stop. Source is "pod-monitor" or
// "host-manual"; Policy is snapshotted at fire time.
type LastStopEvent struct {
	StoppedAt        string           `json:"stoppedAt"`
	ArmedAt          string           `json:"armedAt,omitempty"`
	GraceSeconds     int64            `json:"graceSeconds"`
	Source           string           `json:"source,omitempty"`
	Reason           string           `json:"reason"`
	CloudContextName string           `json:"cloudContextName,omitempty"`
	Policy           *IdlePolicy      `json:"policy,omitempty"`
	Markers          []LastStopMarker `json:"markers,omitempty"`
}

// IdlePolicy uses seconds so the frontend never parses Go durations.
type IdlePolicy struct {
	TimeoutSeconds   int64  `json:"timeoutSeconds"`
	WorkingHours     string `json:"workingHours,omitempty"`
	Timezone         string `json:"timezone,omitempty"`
	IdleTrafficBytes int64  `json:"idleTrafficBytes,omitempty"`
}

// LastStopMarker is a marker's state when the auto-stop fired.
type LastStopMarker struct {
	Name           string `json:"name"`
	Idle           bool   `json:"idle"`
	Reason         string `json:"reason,omitempty"`
	SecondsIdleFor int64  `json:"secondsIdleFor,omitempty"`
}

type IdleMarker struct {
	Name             string             `json:"name"`
	Idle             bool               `json:"idle"`
	Reason           string             `json:"reason,omitempty"`
	SecondsRemaining int64              `json:"secondsRemaining,omitempty"`
	Clients          []IdleMarkerClient `json:"clients,omitempty"`
}

// IdleMarkerClient is populated only by the SSH proxy.
type IdleMarkerClient struct {
	Address    string `json:"address"`
	Bytes      int64  `json:"bytes,omitempty"`
	SecondsAgo int64  `json:"secondsAgo,omitempty"`
}
