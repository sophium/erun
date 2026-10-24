package repository

import (
	"context"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

// auditEventColumns omits the *_parameters columns: they can hold
// caller-supplied credentials and must never be read back.
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

// AuditEventCursor is a keyset position in List's (created_at DESC,
// audit_event_id DESC) order; the zero value starts at the newest row.
type AuditEventCursor struct {
	CreatedAt    time.Time
	AuditEventID string
}

func (c AuditEventCursor) isZero() bool {
	return c.CreatedAt.IsZero() && c.AuditEventID == ""
}

// String encodes the cursor as an opaque token.
func (c AuditEventCursor) String() string {
	if c.isZero() {
		return ""
	}
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "," + c.AuditEventID
}

// ParseAuditEventCursor decodes a cursor token; empty decodes to the zero cursor.
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

// AuditEventFilter narrows a page of the caller's tenant audit trail.
type AuditEventFilter struct {
	Since      time.Time
	Until      time.Time
	ErunUserID string
	Type       model.AuditEventType
	APIMethod  string
	APIPath    string
	Cursor     AuditEventCursor
	Limit      int
}

// AuditEventPage is one newest-first page; NextCursor is empty at the end.
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

// List returns one page of the tenant's audit trail, newest first. tenant_id is
// filtered explicitly because erun_operations' RLS policy is unconditional.
func (r *AuditEventRepository) List(ctx context.Context, filter AuditEventFilter) (AuditEventPage, error) {
	limit := filter.Limit
	switch {
	case limit <= 0:
		limit = defaultAuditEventLimit
	case limit > maxAuditEventLimit:
		limit = maxAuditEventLimit
	}

	var events []model.AuditEvent
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query, args := auditEventListQuery(securityContext.TenantID, filter, limit)
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

// auditEventListQuery fetches limit+1 rows so List can detect a next page.
func auditEventListQuery(tenantID string, filter AuditEventFilter, limit int) (string, []any) {
	query := `SELECT ` + auditEventColumns + ` FROM audit_events WHERE tenant_id = ?`
	args := []any{tenantID}
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
