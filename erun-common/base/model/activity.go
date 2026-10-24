package model

import (
	"time"
)

type EnvironmentIdlePolicy struct {
	Timeout          time.Duration `json:"timeout"`
	WorkingHours     string        `json:"workingHours"`
	Timezone         string        `json:"timezone,omitempty"`
	IdleTrafficBytes int64         `json:"idleTrafficBytes"`
}

type EnvironmentActivityParams struct {
	Tenant        string
	Environment   string
	Kind          string
	Seen          bool
	Bytes         int64
	ClientUpdates []EnvironmentActivityClientUpdate
	Now           time.Time
}

// EnvironmentActivityClientUpdate carries a per-client byte delta for attribution.
type EnvironmentActivityClientUpdate struct {
	Address string
	Bytes   int64
}

// Timestamps use omitzero: omitempty never omits a time.Time, so it would render a
// never-set field as the zero instant.

// EnvironmentActivitySnapshot is the on-disk record for one activity kind; Clients is bounded.
type EnvironmentActivitySnapshot struct {
	LastActivity time.Time                            `json:"lastActivity,omitzero"`
	LastSeen     time.Time                            `json:"lastSeen,omitzero"`
	Bytes        int64                                `json:"bytes,omitempty"`
	Clients      map[string]EnvironmentActivityClient `json:"clients,omitempty"`
}

type EnvironmentActivityClient struct {
	Bytes        int64     `json:"bytes,omitempty"`
	LastActivity time.Time `json:"lastActivity,omitzero"`
}

type EnvironmentIdleMarker struct {
	Name             string                        `json:"name"`
	Idle             bool                          `json:"idle"`
	Reason           string                        `json:"reason,omitempty"`
	SecondsRemaining int64                         `json:"secondsRemaining,omitempty"`
	LastActivity     time.Time                     `json:"lastActivity,omitzero"`
	LastSeen         time.Time                     `json:"lastSeen,omitzero"`
	Clients          []EnvironmentIdleMarkerClient `json:"clients,omitempty"`
}

// EnvironmentIdleMarkerClient is the marker's per-client view for renderers.
type EnvironmentIdleMarkerClient struct {
	Address      string    `json:"address"`
	Bytes        int64     `json:"bytes,omitempty"`
	LastActivity time.Time `json:"lastActivity,omitzero"`
	SecondsAgo   int64     `json:"secondsAgo,omitempty"`
}

type EnvironmentIdleStatus struct {
	Policy              EnvironmentIdlePolicy                  `json:"policy"`
	OutsideWorkingHours bool                                   `json:"outsideWorkingHours"`
	ManagedCloud        bool                                   `json:"managedCloud"`
	StopEligible        bool                                   `json:"stopEligible"`
	StopBlockedReason   string                                 `json:"stopBlockedReason,omitempty"`
	StopError           string                                 `json:"stopError,omitempty"`
	SecondsUntilStop    int64                                  `json:"secondsUntilStop,omitempty"`
	Markers             []EnvironmentIdleMarker                `json:"markers"`
	Activity            map[string]EnvironmentActivitySnapshot `json:"activity,omitempty"`
	// Leases say what is deferring auto-stop.
	Leases []EnvironmentActivityLease `json:"leases,omitempty"`
	// StopPendingSince is when the auto-stop grace period was armed (RFC3339).
	StopPendingSince       string `json:"stopPendingSince,omitempty"`
	SecondsUntilForcedStop int64  `json:"secondsUntilForcedStop,omitempty"`
	GracePeriodSeconds     int64  `json:"gracePeriodSeconds,omitempty"`
}
