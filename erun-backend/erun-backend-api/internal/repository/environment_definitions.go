package repository

import (
	"context"
	"encoding/json"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

const environmentDefinitionColumns = `tenant_id, environment_id, revision, definition, written_by_user_id, created_at, updated_at`

// EnvironmentDefinitionRepository stores one definition row per environment.
type EnvironmentDefinitionRepository struct {
	txs *TxManager
}

func NewEnvironmentDefinitionRepository(txs *TxManager) *EnvironmentDefinitionRepository {
	return &EnvironmentDefinitionRepository{txs: txs}
}

// Upsert stores definition and advances its revision (1 for a new row). The
// increment is in SQL so overlapping uploads cannot publish the same revision.
func (r *EnvironmentDefinitionRepository) Upsert(ctx context.Context, environmentID string, definition json.RawMessage) (model.EnvironmentDefinition, error) {
	var stored model.EnvironmentDefinition
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// tenant and writer are database-defaulted from the security context, so
		// require one rather than write an ownerless row.
		if _, err := security.RequiredFromContext(ctx); err != nil {
			return ErrMissingSecurityContext
		}
		return tx.NewRaw(`
			INSERT INTO environment_definitions (environment_id, revision, definition)
			VALUES (?, 1, ?)
			ON CONFLICT (tenant_id, environment_id) DO UPDATE
			   SET revision   = environment_definitions.revision + 1,
			       definition = EXCLUDED.definition
			RETURNING `+environmentDefinitionColumns+`
		`, environmentID, definition).Scan(ctx, &stored)
	})
	return stored, err
}

// Get returns one environment's stored definition. tenant_id is filtered
// explicitly because erun_operations' RLS policy is unconditional.
func (r *EnvironmentDefinitionRepository) Get(ctx context.Context, environmentID string) (model.EnvironmentDefinition, error) {
	var stored model.EnvironmentDefinition
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		return tx.NewRaw(`
			SELECT `+environmentDefinitionColumns+`
			  FROM environment_definitions
			 WHERE tenant_id = ?
			   AND environment_id = ?
		`, securityContext.TenantID, environmentID).Scan(ctx, &stored)
	})
	return stored, err
}
