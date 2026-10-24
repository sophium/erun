package repository

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

const environmentEventColumns = `environment_event_id, environment_event_seq, tenant_id, environment_id, kind, detail, occurred_at, created_at`

const (
	defaultEnvironmentEventLimit = 200
	maxEnvironmentEventLimit     = 1000
)

// EnvironmentEventCursor is the database sequence of the last event seen; the
// reader resumes after it. A sequence, unlike a timestamp or uuidv7, is totally
// ordered. The zero value means the beginning of the log.
type EnvironmentEventCursor struct {
	Seq int64
}

func (c EnvironmentEventCursor) isZero() bool {
	return c.Seq <= 0
}

// String encodes the cursor as an opaque token; the zero cursor encodes to "".
func (c EnvironmentEventCursor) String() string {
	if c.isZero() {
		return ""
	}
	return strconv.FormatInt(c.Seq, 10)
}

// ParseEnvironmentEventCursor decodes a cursor token; empty decodes to zero. A
// malformed token is refused, since treating it as zero would replay the log.
func ParseEnvironmentEventCursor(token string) (EnvironmentEventCursor, error) {
	if token == "" {
		return EnvironmentEventCursor{}, nil
	}
	seq, err := strconv.ParseInt(token, 10, 64)
	if err != nil || seq <= 0 {
		return EnvironmentEventCursor{}, ErrInvalidInput
	}
	return EnvironmentEventCursor{Seq: seq}, nil
}

// EnvironmentEventFilter narrows one forward page of the log.
type EnvironmentEventFilter struct {
	EnvironmentID string
	Cursor        EnvironmentEventCursor
	Limit         int
}

// EnvironmentEventPage is one forward page, oldest first; NextCursor is empty at the end.
type EnvironmentEventPage struct {
	Events     []model.EnvironmentEvent
	NextCursor string
}

type EnvironmentEventRepository struct {
	txs *TxManager
}

func NewEnvironmentEventRepository(txs *TxManager) *EnvironmentEventRepository {
	return &EnvironmentEventRepository{txs: txs}
}

// Append records one event; tenant and sequence are assigned by the database.
func (r *EnvironmentEventRepository) Append(ctx context.Context, event model.EnvironmentEvent) (model.EnvironmentEvent, error) {
	// NULL falls back to the column's DEFAULT (receipt time).
	var occurredAt *time.Time
	if !event.OccurredAt.IsZero() {
		occurredAt = &event.OccurredAt
	}
	var appended model.EnvironmentEvent
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			INSERT INTO environment_events (environment_id, kind, detail, occurred_at)
			VALUES (?, ?, NULLIF(?, ''), COALESCE(?::timestamptz, NOW()))
			RETURNING `+environmentEventColumns+`
		`, event.EnvironmentID, event.Kind, event.Detail, occurredAt).Scan(ctx, &appended)
		return classifyEnvironmentEventError(err)
	})
	return appended, err
}

// classifyEnvironmentEventError maps constraint violations to sentinel errors;
// an invisible environmentId fails the FK like a missing one.
func classifyEnvironmentEventError(err error) error {
	code, ok := pgErrorCode(err)
	if !ok {
		return err
	}
	switch code {
	case pgerrcode.ForeignKeyViolation:
		return ErrNotFound
	case pgerrcode.NotNullViolation, pgerrcode.CheckViolation:
		return ErrInvalidInput
	default:
		return err
	}
}

// Read returns one oldest-first page after the cursor, so a reconnecting reader
// gets its backlog in order. tenant_id is filtered explicitly because
// erun_operations' RLS policy is unconditional.
func (r *EnvironmentEventRepository) Read(ctx context.Context, filter EnvironmentEventFilter) (EnvironmentEventPage, error) {
	limit := filter.Limit
	switch {
	case limit <= 0:
		limit = defaultEnvironmentEventLimit
	case limit > maxEnvironmentEventLimit:
		limit = maxEnvironmentEventLimit
	}

	var events []model.EnvironmentEvent
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query := `SELECT ` + environmentEventColumns + ` FROM environment_events WHERE tenant_id = ?`
		args := []any{securityContext.TenantID}
		if filter.EnvironmentID != "" {
			query += ` AND environment_id = ?`
			args = append(args, filter.EnvironmentID)
		}
		if !filter.Cursor.isZero() {
			// Strictly greater: the cursor names an event already delivered.
			query += ` AND environment_event_seq > ?`
			args = append(args, filter.Cursor.Seq)
		}
		query += ` ORDER BY environment_event_seq ASC LIMIT ?`
		args = append(args, limit+1)
		return tx.NewRaw(query, args...).Scan(ctx, &events)
	})
	if err != nil {
		return EnvironmentEventPage{}, err
	}

	page := EnvironmentEventPage{Events: events}
	if len(events) > limit {
		page.Events = events[:limit]
		last := page.Events[len(page.Events)-1]
		page.NextCursor = EnvironmentEventCursor{Seq: last.EnvironmentEventSeq}.String()
	}
	return page, nil
}
