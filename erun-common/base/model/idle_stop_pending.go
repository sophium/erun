package model

import (
	"time"
)

// EnvironmentStopPending is the on-disk record that arms the grace-period warning.
type EnvironmentStopPending struct {
	Since            time.Time               `json:"since"`
	GraceSeconds     int64                   `json:"graceSeconds"`
	CloudContextName string                  `json:"cloudContextName,omitempty"`
	ReasonSummary    string                  `json:"reasonSummary,omitempty"`
	Markers          []EnvironmentIdleMarker `json:"markers,omitempty"`
	// Snapshotted at arm time so history stays interpretable after policy edits.
	Policy EnvironmentIdlePolicy `json:"policy"`
}

// EnvironmentStopHistoryEntry is one stop audit row; ArmedAt is set only on pod-monitor stops.
type EnvironmentStopHistoryEntry struct {
	StoppedAt        time.Time                      `json:"stoppedAt"`
	ArmedAt          time.Time                      `json:"armedAt,omitzero"`
	GraceSeconds     int64                          `json:"graceSeconds"`
	Source           string                         `json:"source,omitempty"`
	Reason           string                         `json:"reason"`
	CloudContextName string                         `json:"cloudContextName,omitempty"`
	Policy           EnvironmentIdlePolicy          `json:"policy,omitzero"`
	Markers          []EnvironmentStopHistoryMarker `json:"markers,omitempty"`
}

// EnvironmentStopHistoryMarker is the audit subset of EnvironmentIdleMarker.
type EnvironmentStopHistoryMarker struct {
	Name           string `json:"name"`
	Idle           bool   `json:"idle"`
	Reason         string `json:"reason,omitempty"`
	SecondsIdleFor int64  `json:"secondsIdleFor,omitempty"`
}

// MaybeArmOrFireIdleStopParams are the inputs to MaybeArmOrFireIdleStop.
type MaybeArmOrFireIdleStopParams struct {
	Tenant           string
	Environment      string
	Status           EnvironmentIdleStatus
	CloudContextName string
	ReasonSummary    string
	Now              time.Time
}

// MaybeArmOrFireIdleStopResult's State is empty on Skip and Fire.
type MaybeArmOrFireIdleStopResult struct {
	Action           string
	State            EnvironmentStopPending
	SecondsRemaining int64
}
