package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type EnvironmentRepository struct {
	txs *TxManager
}

const environmentColumns = `environment_id, tenant_id, name, type, kubernetes_context, context_id, runtime_version, created_at, updated_at`

func NewEnvironmentRepository(txs *TxManager) *EnvironmentRepository {
	return &EnvironmentRepository{txs: txs}
}

// Create inserts a new environment for the caller's tenant and returns the
// persisted row. Only the operator-authored columns are written; environment_id,
// tenant_id, and the timestamps are owned by the database (the tenant_id DEFAULT +
// RLS bind the row to the caller's tenant automatically), so they are excluded
// from the Column list and populated by Returning("*"). The env references its
// context by context_id; the composite (tenant_id, context_id) foreign key
// enforces that the context belongs to the same tenant, so a context_id from
// another tenant surfaces as a foreign-key-violation error here.
func (r *EnvironmentRepository) Create(ctx context.Context, environment model.Environment) (model.Environment, error) {
	created := environment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewInsert().
			Model(&created).
			Column("name", "type", "kubernetes_context", "context_id", "runtime_version").
			Returning("*").
			Scan(ctx)
	})
	return created, err
}

func (r *EnvironmentRepository) List(ctx context.Context) ([]model.Environment, error) {
	var environments []model.Environment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT `+environmentColumns+`
			  FROM environments
			 ORDER BY name ASC, environment_id ASC
		`).Scan(ctx, &environments)
	})
	return environments, err
}

func (r *EnvironmentRepository) Get(ctx context.Context, environmentID string) (model.Environment, error) {
	var environment model.Environment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT `+environmentColumns+`
			  FROM environments
			 WHERE environment_id = ?
		`, environmentID).Scan(ctx, &environment)
		return normalizeNoRows(err)
	})
	return environment, err
}
