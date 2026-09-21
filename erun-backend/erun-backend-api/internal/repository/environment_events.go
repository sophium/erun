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

// EnvironmentEventCursor names a position in the log rather than a row in it:
// the reader resumes *after* the event it last saw, so a cursor never has to
// agree about which row existed, only about where the stream stood. The zero
// value means "from the beginning of the log".
//
// It is the database-assigned sequence, not the event's uuidv7 id: a cursor
// has to be totally ordered and stable across replays, and neither a
// timestamp (which collides within a millisecond) nor a uuidv7 (whose tail is
// random inside one) gives that on its own. A sequence does, and it is a
// plain integer to compare rather than a composite pair to break ties on.
type EnvironmentEventCursor struct {
	Seq int64
}

func (c EnvironmentEventCursor) isZero() bool {
	return c.Seq <= 0
}

// String encodes the cursor as an opaque token for a caller to hand back to
// resume the stream. The zero cursor encodes to the empty string, which is
// also what parses back to it.
func (c EnvironmentEventCursor) String() string {
	if c.isZero() {
		return ""
	}
	return strconv.FormatInt(c.Seq, 10)
}

// ParseEnvironmentEventCursor decodes a token previously returned by Read. An
// empty token decodes to the zero cursor. A token that is not a positive
// integer is refused rather than treated as zero: silently restarting the
// stream from the beginning would replay the whole log to a client that asked
// to resume, which is the one thing a resume cursor must never do.
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

// EnvironmentEventFilter narrows one forward page of the log. EnvironmentID
// is optional and narrows the stream to one environment; Cursor is where the
// reader last reached.
type EnvironmentEventFilter struct {
	EnvironmentID string
	Cursor        EnvironmentEventCursor
	// Limit caps the page size; non-positive defaults to
	// defaultEnvironmentEventLimit and any value above
	// maxEnvironmentEventLimit is capped there.
	Limit int
}

// EnvironmentEventPage is one forward page of the log, oldest first.
// NextCursor is empty when the page reached the end of the log as it stood
// when this read ran.
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

// Append records one event, taking the next position in the log. The tenant
// is the database default from the transaction's security context, the same
// as every other tenant-owned insert; the sequence is the identity column's
// to assign, so it is neither accepted nor generated here.
func (r *EnvironmentEventRepository) Append(ctx context.Context, event model.EnvironmentEvent) (model.EnvironmentEvent, error) {
	// A reporter that knows when the event actually happened supplies it; one
	// that only knows it now gets receipt time. NULL is what the column's own
	// DEFAULT would have supplied, made explicit so both paths are one INSERT.
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

// classifyEnvironmentEventError maps environment_events' foreign key and
// CHECK constraints onto the repository's sentinel errors, mirroring
// classifyGateRunError: an environmentId the caller's tenant cannot see fails
// the same foreign key check whether it genuinely doesn't exist or just isn't
// this tenant's.
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

// Read returns one forward page of the caller's tenant's log: everything
// after the cursor, oldest first. Scoped explicitly by tenant_id from the
// security context rather than left to RLS, because erun_operations' policy
// is unconditional and an OPERATIONS caller's empty filter would otherwise
// read every tenant's log.
//
// Oldest-first is the opposite direction from the newest-first pagination
// audit_events and builds use, and deliberately so: a reader that just
// reconnected wants the backlog it has not seen in the order it happened.
// Handing it the newest events first would make it buffer and reverse a page
// to find its own position, which is the cursor's whole job.
//
// A cursor older than anything the log still holds is not an error: it reads
// from the oldest surviving event forward. Nothing prunes this log today, so
// that case cannot arise yet -- if retention is ever added, a cursor pointing
// into a pruned range becomes a silent skip and will need an explicit answer
// here.
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
			// Strictly greater: the cursor names the position already
			// delivered, so a reader that hands back the last event it saw
			// must not be sent that same event again.
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
