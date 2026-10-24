package model

import (
	"time"

	"github.com/uptrace/bun"
)

// EnvironmentEventKind is closed: add a kind only together with its producer.
type EnvironmentEventKind string

const (
	EnvironmentEventStatusChanged        EnvironmentEventKind = "environment-status-changed"
	EnvironmentEventSessionAwaitingInput EnvironmentEventKind = "session-awaiting-input"
	EnvironmentEventJobFinished          EnvironmentEventKind = "job-finished"
)

// EnvironmentEvent is one append-only row of the sequenced log.
type EnvironmentEvent struct {
	bun.BaseModel      `bun:"table:environment_events,alias:e"`
	EnvironmentEventID string `json:"environmentEventId" bun:"environment_event_id,pk,scanonly"`
	// EnvironmentEventSeq is database-assigned; readers resume after it and order by it.
	EnvironmentEventSeq int64                `json:"seq" bun:"environment_event_seq,scanonly"`
	TenantID            string               `json:"tenantId" bun:"tenant_id,scanonly"`
	EnvironmentID       string               `json:"environmentId" bun:"environment_id"`
	Kind                EnvironmentEventKind `json:"kind" bun:"kind"`
	Detail              string               `json:"detail,omitempty" bun:"detail,nullzero"`
	// OccurredAt is when the producer observed the event, which a late report puts before CreatedAt.
	OccurredAt time.Time `json:"occurredAt" bun:"occurred_at,scanonly"`
	CreatedAt  time.Time `json:"createdAt" bun:"created_at,scanonly"`
}
