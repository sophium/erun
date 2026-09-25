package repository

import (
	"context"
	"encoding/json"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

const environmentDefinitionColumns = `tenant_id, environment_id, revision, definition, written_by_user_id, created_at, updated_at`

// EnvironmentDefinitionRepository owns the definition store: one row per
// environment, replaced on each upload with its revision advanced.
type EnvironmentDefinitionRepository struct {
	txs *TxManager
}

func NewEnvironmentDefinitionRepository(txs *TxManager) *EnvironmentDefinitionRepository {
	return &EnvironmentDefinitionRepository{txs: txs}
}

// Upsert stores definition as the environment's current one, advancing the
// revision by one — or writing revision 1 for an environment that has none yet,
// which is how registration writes the definition alongside the adopt call.
//
// The increment is done in SQL rather than read-then-write so two uploads that
// overlap cannot both publish the same revision: the row is locked by the
// upsert, and the second waits and then advances past the first.
func (r *EnvironmentDefinitionRepository) Upsert(ctx context.Context, environmentID string, definition json.RawMessage) (model.EnvironmentDefinition, error) {
	var stored model.EnvironmentDefinition
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// The tenant and the writer are both database-defaulted from the
		// transaction's security context (erun_current_tenant_id /
		// erun_current_user_id), so the insert names neither. Requiring the
		// context here is what makes that safe: an unwired caller fails as an
		// internal error rather than writing a row with no owner.
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

// Get returns the stored definition for one environment.
//
// It is scoped explicitly by the caller's tenant id rather than left to RLS:
// erun_operations' policy is unconditional, so an OPERATIONS caller naming
// another tenant's environment id would otherwise read that tenant's
// definition — the same reasoning AISessionRepository.List records.
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
