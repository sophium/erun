package model

import (
	"time"

	"github.com/uptrace/bun"
)

// EnvironmentEventKind is the closed vocabulary of events the log records.
// It is deliberately smaller than "everything that happens to an
// environment": a kind exists once something real reports it and something
// real reads it, so the log cannot accumulate kinds with no producer or no
// consumer. Extending it means adding a producer in the same change.
type EnvironmentEventKind string

const (
	// EnvironmentEventStatusChanged is the environment's own lifecycle moving
	// (created, deployed, stopped, deleted) -- the transition an operator
	// watching a console would most want pushed rather than polled.
	EnvironmentEventStatusChanged EnvironmentEventKind = "environment-status-changed"
	// EnvironmentEventSessionAwaitingInput is an AI session in this
	// environment reaching the state erun-common calls AwaitingInput: the
	// agent has stopped and is waiting on a human.
	EnvironmentEventSessionAwaitingInput EnvironmentEventKind = "session-awaiting-input"
	// EnvironmentEventJobFinished is an environment job reaching a terminal
	// state (a deploy that succeeded or failed), the environment-side signal
	// behind "deploy finished".
	EnvironmentEventJobFinished EnvironmentEventKind = "job-finished"
)

// EnvironmentEvent is one row of the sequenced log. Events are append-only
// and never updated; a correction is reported as a new event.
type EnvironmentEvent struct {
	bun.BaseModel `bun:"table:environment_events,alias:e"`
	// EnvironmentEventID is the API-visible identity of the event itself.
	EnvironmentEventID string `json:"environmentEventId" bun:"environment_event_id,pk,scanonly"`
	// EnvironmentEventSeq is the log position this event occupies, assigned
	// by the database. It is what a reader names to resume *after* this
	// event, and the only field reads order by. Read-only: an insert that
	// carried one would be claiming a position rather than taking the next.
	EnvironmentEventSeq int64                `json:"seq" bun:"environment_event_seq,scanonly"`
	TenantID            string               `json:"tenantId" bun:"tenant_id,scanonly"`
	EnvironmentID       string               `json:"environmentId" bun:"environment_id"`
	Kind                EnvironmentEventKind `json:"kind" bun:"kind"`
	// Detail is the event's own payload as serialized text (compact JSON for
	// structured input). The log stores it uninterpreted.
	Detail string `json:"detail,omitempty" bun:"detail,nullzero"`
	// OccurredAt is when the producer observed the event; CreatedAt is when
	// the platform recorded it. A report can arrive after the fact, so they
	// are not the same field.
	OccurredAt time.Time `json:"occurredAt" bun:"occurred_at,scanonly"`
	CreatedAt  time.Time `json:"createdAt" bun:"created_at,scanonly"`
}
