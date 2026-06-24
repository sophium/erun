package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type ContextRepository struct {
	txs *TxManager
}

const contextColumns = `context_id, tenant_id, name, provider, cloud_provider_alias, region, instance_id, public_ip, instance_type, disk_type, disk_size_gb, kubernetes_context, created_at, updated_at`

func NewContextRepository(txs *TxManager) *ContextRepository {
	return &ContextRepository{txs: txs}
}

// Create inserts a new cloud context for the caller's tenant and returns the
// persisted row. Only the operator-authored columns are written; context_id,
// tenant_id, and the timestamps are owned by the database (the tenant_id
// DEFAULT + RLS bind the row to the caller's tenant automatically), so they are
// excluded from the Column list and populated by Returning("*").
func (r *ContextRepository) Create(ctx context.Context, cloudContext model.Context) (model.Context, error) {
	created := cloudContext
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewInsert().
			Model(&created).
			Column("name", "provider", "cloud_provider_alias", "region", "instance_type", "disk_type", "disk_size_gb", "kubernetes_context").
			Returning("*").
			Scan(ctx)
	})
	return created, err
}

func (r *ContextRepository) List(ctx context.Context) ([]model.Context, error) {
	var contexts []model.Context
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT `+contextColumns+`
			  FROM contexts
			 ORDER BY name ASC, context_id ASC
		`).Scan(ctx, &contexts)
	})
	return contexts, err
}

func (r *ContextRepository) Get(ctx context.Context, contextID string) (model.Context, error) {
	var cloudContext model.Context
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT `+contextColumns+`
			  FROM contexts
			 WHERE context_id = ?
		`, contextID).Scan(ctx, &cloudContext)
		return normalizeNoRows(err)
	})
	return cloudContext, err
}
