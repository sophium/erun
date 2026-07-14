package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type EnvironmentRepository struct {
	txs *TxManager
}

const environmentColumns = `environment_id, tenant_id, name, type, kubernetes_context, context_id, runtime_version, status, provision_error, created_at, updated_at`

func NewEnvironmentRepository(txs *TxManager) *EnvironmentRepository {
	return &EnvironmentRepository{txs: txs}
}

// Create inserts a new environment for the caller's tenant. A context that
// belongs to another tenant is rejected, keeping context references
// tenant-isolated.
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

// Count returns how many environments the caller's tenant has, for enforcing
// the tenant's environment-count quota cap.
func (r *EnvironmentRepository) Count(ctx context.Context) (int, error) {
	var count int
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		count, err = tx.NewSelect().Model((*model.Environment)(nil)).Count(ctx)
		return err
	})
	return count, err
}

// UpdateProvisioningStatus persists an environment's provisioning-lifecycle
// transition (registered → provisioning → running/failed), clearing the error
// on any non-failed state. Mirrors the contexts provisioning-result update; RLS
// keeps the write scoped to the caller's tenant.
func (r *EnvironmentRepository) UpdateProvisioningStatus(ctx context.Context, environmentID, status, provisionError string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       provision_error = NULLIF(?, '')
			 WHERE environment_id = ?
		`, status, provisionError, environmentID).Exec(ctx)
		return err
	})
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
