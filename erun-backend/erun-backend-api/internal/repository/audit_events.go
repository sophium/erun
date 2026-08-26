package repository

import (
	"context"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

// auditEventColumns deliberately omits cli_parameters, mcp_tool_parameters,
// and api_parameters: those columns are where a CLI/MCP/API audit caller
// serializes caller-supplied text, and a tool such as
// cloud_inject_aws_credentials takes credentials as arguments. The read path
// must never select them.
const auditEventColumns = `audit_event_id, tenant_id, erun_user_id, external_user_id, external_issuer_id, external_org_id, type, api_method, api_path, cli_command, mcp_tool, created_at`

const (
	defaultAuditEventLimit = 50
	maxAuditEventLimit     = 200
)

type AuditEventRepository struct {
	txs *TxManager
}

func NewAuditEventRepository(txs *TxManager) *AuditEventRepository {
	return &AuditEventRepository{txs: txs}
}

// AuditEventCursor identifies a row's position in the newest-first
// (created_at DESC, audit_event_id DESC) ordering that List always uses. The
// zero value means "start from the newest row" — audit_events is append-only
// and unbounded, so offset pagination would degrade as it grows and would
// skip or repeat rows as new events are appended ahead of a later page.
type AuditEventCursor struct {
	CreatedAt    time.Time
	AuditEventID string
}

func (c AuditEventCursor) isZero() bool {
	return c.CreatedAt.IsZero() && c.AuditEventID == ""
}

// String encodes the cursor as an opaque token for a caller to hand back to
// resume after the last row it saw.
func (c AuditEventCursor) String() string {
	if c.isZero() {
		return ""
	}
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "," + c.AuditEventID
}

// ParseAuditEventCursor decodes a token previously returned by List. An empty
// token decodes to the zero cursor.
func ParseAuditEventCursor(token string) (AuditEventCursor, error) {
	if token == "" {
		return AuditEventCursor{}, nil
	}
	createdAt, auditEventID, ok := strings.Cut(token, ",")
	if !ok || auditEventID == "" {
		return AuditEventCursor{}, ErrInvalidInput
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return AuditEventCursor{}, ErrInvalidInput
	}
	return AuditEventCursor{CreatedAt: parsed, AuditEventID: auditEventID}, nil
}

// AuditEventFilter narrows a page of the caller's tenant audit trail. Every
// field is optional; the zero filter lists the newest page of the tenant's
// whole trail. Since/Until/ErunUserID and the (Type, APIMethod, APIPath)
// combination are the filters the three audit_events indexes were built for.
type AuditEventFilter struct {
	Since      time.Time
	Until      time.Time
	ErunUserID string
	Type       model.AuditEventType
	APIMethod  string
	APIPath    string
	Cursor     AuditEventCursor
	// Limit caps the page size; non-positive defaults to defaultAuditEventLimit
	// and any value above maxAuditEventLimit is capped there.
	Limit int
}

// AuditEventPage is one newest-first page of a tenant's audit trail.
// NextCursor is empty when the page reached the end of the trail.
type AuditEventPage struct {
	Events     []model.AuditEvent
	NextCursor string
}

func (r *AuditEventRepository) LogAuditEvent(ctx context.Context, event model.AuditEvent) error {
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			INSERT INTO audit_events (
				tenant_id,
				erun_user_id,
				external_user_id,
				external_issuer_id,
				external_org_id,
				type,
				api_method,
				api_path,
				api_parameters,
				cli_command,
				cli_parameters,
				mcp_tool,
				mcp_tool_parameters,
				created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			event.TenantID,
			event.ErunUserID,
			event.ExternalUserID,
			event.ExternalIssuerID,
			nullString(event.ExternalOrgID),
			string(event.Type),
			nullString(event.APIMethod),
			nullString(event.APIPath),
			nullString(event.APIParameters),
			nullString(event.CLICommand),
			nullString(event.CLIParameters),
			nullString(event.MCPTool),
			nullString(event.MCPToolParameters),
			createdAt,
		).Exec(ctx)
		return err
	})
}

// List returns one page of the caller's tenant audit trail, newest first.
// RLS scopes rows to the tenant (or, under erun_operations, every tenant); the
// filters and cursor here are the same tenant-owned SQL every other repository
// runs through TxManager.
func (r *AuditEventRepository) List(ctx context.Context, filter AuditEventFilter) (AuditEventPage, error) {
	limit := filter.Limit
	switch {
	case limit <= 0:
		limit = defaultAuditEventLimit
	case limit > maxAuditEventLimit:
		limit = maxAuditEventLimit
	}

	query, args := auditEventListQuery(filter, limit)
	var events []model.AuditEvent
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(query, args...).Scan(ctx, &events)
	})
	if err != nil {
		return AuditEventPage{}, err
	}

	page := AuditEventPage{Events: events}
	if len(events) > limit {
		page.Events = events[:limit]
		last := page.Events[len(page.Events)-1]
		page.NextCursor = AuditEventCursor{CreatedAt: last.CreatedAt, AuditEventID: last.AuditEventID}.String()
	}
	return page, nil
}

// auditEventListQuery builds the SELECT for List. It fetches one row beyond
// limit so a next page can be reported without a second round trip; List
// trims that extra row back off.
func auditEventListQuery(filter AuditEventFilter, limit int) (string, []any) {
	query := `SELECT ` + auditEventColumns + ` FROM audit_events WHERE TRUE`
	var args []any
	if !filter.Since.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		query += ` AND created_at <= ?`
		args = append(args, filter.Until)
	}
	if filter.ErunUserID != "" {
		query += ` AND erun_user_id = ?`
		args = append(args, filter.ErunUserID)
	}
	if filter.Type != "" {
		query += ` AND type = ?`
		args = append(args, string(filter.Type))
	}
	if filter.APIMethod != "" {
		query += ` AND api_method = ?`
		args = append(args, filter.APIMethod)
	}
	if filter.APIPath != "" {
		query += ` AND api_path = ?`
		args = append(args, filter.APIPath)
	}
	if !filter.Cursor.isZero() {
		query += ` AND (created_at, audit_event_id) < (?, ?)`
		args = append(args, filter.Cursor.CreatedAt, filter.Cursor.AuditEventID)
	}
	query += ` ORDER BY created_at DESC, audit_event_id DESC LIMIT ?`
	args = append(args, limit+1)
	return query, args
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
