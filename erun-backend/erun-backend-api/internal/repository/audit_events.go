package repository

import (
	"context"
	"database/sql"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

type AuditEventRepository struct {
	db *bun.DB
}

func NewAuditEventRepository(db *sql.DB) *AuditEventRepository {
	return NewAuditEventRepositoryForDialect(db, DialectPostgres)
}

func NewAuditEventRepositoryForDialect(db *sql.DB, _ Dialect) *AuditEventRepository {
	return &AuditEventRepository{db: bun.NewDB(db, pgdialect.New())}
}

func (r *AuditEventRepository) LogAuditEvent(ctx context.Context, event model.AuditEvent) error {
	_, err := r.db.NewInsert().
		Model(&event).
		Column(
			"tenant_id",
			"erun_user_id",
			"external_user_id",
			"external_issuer_id",
			"type",
			"api_method",
			"api_path",
			"cli_command",
			"cli_parameters",
			"mcp_tool",
			"mcp_tool_parameters",
			"created_at",
		).
		Exec(ctx)
	return err
}
