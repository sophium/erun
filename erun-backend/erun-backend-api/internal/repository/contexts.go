package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type ContextRepository struct {
	txs *TxManager
}

const contextColumns = `context_id, tenant_id, name, provider, cloud_provider_alias, region, instance_id, public_ip, instance_type, disk_type, disk_size_gb, kubernetes_context, status, provision_error, created_at, updated_at`

func NewContextRepository(txs *TxManager) *ContextRepository {
	return &ContextRepository{txs: txs}
}

// Create inserts a new cloud context for the caller's tenant and returns the
// persisted row. Only operator-authored columns are written; context_id,
// tenant_id, and the timestamps are database-owned (the tenant_id DEFAULT and
// RLS bind the row to the caller's tenant), so the database generates them.
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

// UpdateProvisioningResult records the outcome of a provisioning run: on success
// the executor passes status="running" with the resolved instance_id/public_ip
// and an empty error; on failure status="failed" with the reason. RLS scopes the
// UPDATE to the caller's tenant. Empty instance/IP/error values normalize to
// NULL so a failed run does not leave a stale instance id.
func (r *ContextRepository) UpdateProvisioningResult(ctx context.Context, contextID, status, instanceID, publicIP, provisionError string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			UPDATE contexts
			   SET status = ?,
			       instance_id = NULLIF(?, ''),
			       public_ip = NULLIF(?, ''),
			       provision_error = NULLIF(?, '')
			 WHERE context_id = ?
		`, status, instanceID, publicIP, provisionError, contextID).Exec(ctx)
		return err
	})
}
