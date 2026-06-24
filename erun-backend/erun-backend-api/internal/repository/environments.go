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
