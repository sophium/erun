package model

import (
	"fmt"
	"strings"
	"time"
)

// EnvironmentActivityLeaseHolder identifies a lease's claimant. Tenant and User come from
// resolved auth, never caller input.
type EnvironmentActivityLeaseHolder struct {
	Orchestrator string `json:"orchestrator,omitempty"`
	Tenant       string `json:"tenant,omitempty"`
	User         string `json:"user,omitempty"`
}

// String renders the holder for errors and busy details.
func (h EnvironmentActivityLeaseHolder) String() string {
	parts := make([]string, 0, 3)
	if v := strings.TrimSpace(h.Orchestrator); v != "" {
		parts = append(parts, "orchestrator "+v)
	}
	if v := strings.TrimSpace(h.User); v != "" {
		parts = append(parts, "user "+v)
	}
	if v := strings.TrimSpace(h.Tenant); v != "" {
		parts = append(parts, "tenant "+v)
	}
	if len(parts) == 0 {
		return "an unnamed holder"
	}
	return strings.Join(parts, ", ")
}

// EnvironmentActivityLease is one held claim on an environment.
type EnvironmentActivityLease struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// PID is 0 when there is no process to watch; a dead PID reclaims the lease.
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	RenewedAt time.Time `json:"renewedAt,omitzero"`
	ExpiresAt time.Time `json:"expiresAt"`
	// Scope and Exclusive are set only on an exclusive claim.
	Scope     string                         `json:"scope,omitempty"`
	Exclusive bool                           `json:"exclusive,omitempty"`
	Holder    EnvironmentActivityLeaseHolder `json:"holder,omitempty"`
}

// EnvironmentOperatorPresentError refuses a fresh exclusive take while an operator is present;
// renewals are never refused.
type EnvironmentOperatorPresentError struct {
	Reason string
}

func (e *EnvironmentOperatorPresentError) Error() string {
	return fmt.Sprintf("refusing exclusive lease: %s", e.Reason)
}
