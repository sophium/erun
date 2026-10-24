package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

const aiSessionColumns = `tenant_id, environment_id, session_id, tool, event, occurred_at, exit_code, exit_reason, created_at, updated_at`

type AISessionRepository struct {
	txs *TxManager
}

func NewAISessionRepository(txs *TxManager) *AISessionRepository {
	return &AISessionRepository{txs: txs}
}

// Record upserts the latest event for one (environment, session); an event
// without Tool keeps the previously reported one.
func (r *AISessionRepository) Record(ctx context.Context, event model.AISessionEvent) (model.AISessionEvent, error) {
	var recorded model.AISessionEvent
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			INSERT INTO ai_sessions (environment_id, session_id, tool, event, occurred_at, exit_code, exit_reason)
			VALUES (?, ?, NULLIF(?, ''), ?, NOW(), ?, NULLIF(?, ''))
			ON CONFLICT (tenant_id, environment_id, session_id) DO UPDATE
			   SET tool        = COALESCE(NULLIF(EXCLUDED.tool, ''), ai_sessions.tool),
			       event       = EXCLUDED.event,
			       occurred_at = EXCLUDED.occurred_at,
			       exit_code   = EXCLUDED.exit_code,
			       exit_reason = EXCLUDED.exit_reason
			RETURNING `+aiSessionColumns+`
		`, event.EnvironmentID, event.SessionID, event.Tool, event.Event, event.ExitCode, event.ExitReason).Scan(ctx, &recorded)
	})
	return recorded, err
}

// List returns one environment's session events sorted by session id. tenant_id
// is filtered explicitly because erun_operations' RLS policy is unconditional.
func (r *AISessionRepository) List(ctx context.Context, environmentID string) ([]model.AISessionEvent, error) {
	var events []model.AISessionEvent
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		return tx.NewRaw(`
			SELECT `+aiSessionColumns+`
			  FROM ai_sessions
			 WHERE tenant_id = ?
			   AND environment_id = ?
			 ORDER BY session_id ASC
		`, securityContext.TenantID, environmentID).Scan(ctx, &events)
	})
	return events, err
}
