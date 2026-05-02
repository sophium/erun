package repository

import (
	"context"
	"database/sql"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type AuditEventRepository struct {
	db      *sql.DB
	dialect Dialect
}

func NewAuditEventRepository(db *sql.DB) *AuditEventRepository {
	return &AuditEventRepository{db: db}
}

func NewAuditEventRepositoryForDialect(db *sql.DB, dialect Dialect) *AuditEventRepository {
	return &AuditEventRepository{db: db, dialect: dialect}
}

func (r *AuditEventRepository) LogAuditEvent(ctx context.Context, event model.AuditEvent) error {
	query := `
		INSERT INTO audit_events (
			tenant_id,
			erun_user_id,
			external_user_id,
			external_issuer_id,
			type,
			api_method,
			api_path,
			cli_command,
			cli_parameters,
			mcp_tool,
			mcp_tool_parameters,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	if r.dialect == DialectPostgres {
		query = NewTxManager(r.db, r.dialect).rebind(query)
	}
	_, err := r.db.ExecContext(ctx, query,
		event.TenantID,
		event.ErunUserID,
		event.ExternalUserID,
		event.ExternalIssuerID,
		event.Type,
		nullableAuditString(event.APIMethod),
		nullableAuditString(event.APIPath),
		nullableAuditString(event.CLICommand),
		nullableAuditString(event.CLIParameters),
		nullableAuditString(event.MCPTool),
		nullableAuditString(event.MCPToolParameters),
		event.CreatedAt,
	)
	return err
}

func nullableAuditString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
