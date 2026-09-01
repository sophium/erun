package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

const aiSessionColumns = `tenant_id, environment_id, session_id, tool, event, occurred_at, exit_code, exit_reason, created_at, updated_at`

type AISessionRepository struct {
	txs *TxManager
}

func NewAISessionRepository(txs *TxManager) *AISessionRepository {
	return &AISessionRepository{txs: txs}
}

// Record upserts the last-reported event for one (environment, session):
// this is the environment's own self-report replacing whatever it last said,
// mirroring eruncommon.RecordAISessionEvent's local-file "one writer, latest
// event wins" contract. An event that omits Tool carries the previously
// reported one forward (COALESCE against the existing row) rather than
// blanking it, matching eruncommon's previouslyReportedAISessionTool.
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

// List returns every recorded session event for one environment, sorted by
// session id for a stable, diffable listing — the DB-backed twin of
// eruncommon.LoadAISessionStatuses.
func (r *AISessionRepository) List(ctx context.Context, environmentID string) ([]model.AISessionEvent, error) {
	var events []model.AISessionEvent
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT `+aiSessionColumns+`
			  FROM ai_sessions
			 WHERE environment_id = ?
			 ORDER BY session_id ASC
		`, environmentID).Scan(ctx, &events)
	})
	return events, err
}
