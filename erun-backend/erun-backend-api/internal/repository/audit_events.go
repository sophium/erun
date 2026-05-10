package repository

import (
	"context"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type AuditEventRepository struct {
	txs *TxManager
}

func NewAuditEventRepository(txs *TxManager) *AuditEventRepository {
	return &AuditEventRepository{txs: txs}
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
				type,
				api_method,
				api_path,
				cli_command,
				cli_parameters,
				mcp_tool,
				mcp_tool_parameters,
				created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			event.TenantID,
			event.ErunUserID,
			event.ExternalUserID,
			event.ExternalIssuerID,
			string(event.Type),
			nullString(event.APIMethod),
			nullString(event.APIPath),
			nullString(event.CLICommand),
			nullString(event.CLIParameters),
			nullString(event.MCPTool),
			nullString(event.MCPToolParameters),
			createdAt,
		).Exec(ctx)
		return err
	})
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
