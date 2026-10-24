package model

import (
	"time"

	"github.com/uptrace/bun"
)

// AISessionEvent is the last turn-boundary event reported for one AI session;
// a later report replaces the row, since only the latest decides the state.
type AISessionEvent struct {
	bun.BaseModel `bun:"table:ai_sessions,alias:ais"`
	TenantID      string    `json:"tenantId" bun:"tenant_id,scanonly"`
	EnvironmentID string    `json:"environmentId" bun:"environment_id"`
	SessionID     string    `json:"sessionId" bun:"session_id"`
	Tool          string    `json:"tool,omitempty" bun:"tool,nullzero"`
	Event         string    `json:"event" bun:"event"`
	OccurredAt    time.Time `json:"occurredAt" bun:"occurred_at,scanonly"`
	ExitCode      *int      `json:"exitCode,omitempty" bun:"exit_code,nullzero"`
	ExitReason    string    `json:"exitReason,omitempty" bun:"exit_reason,nullzero"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
