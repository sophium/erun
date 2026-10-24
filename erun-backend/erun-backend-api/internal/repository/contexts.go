package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type ContextRepository struct {
	txs *TxManager
}

const contextColumns = `context_id, tenant_id, name, provider, cloud_provider_alias, region, instance_id, public_ip, instance_type, disk_type, disk_size_gb, kubernetes_context, max_environments, status, provision_error, created_at, updated_at`

func NewContextRepository(txs *TxManager) *ContextRepository {
	return &ContextRepository{txs: txs}
}

// DefaultContextMaxEnvironments mirrors the contexts.max_environments column default.
const DefaultContextMaxEnvironments = 20

// Create persists a new cloud context bound to the caller's tenant.
func (r *ContextRepository) Create(ctx context.Context, cloudContext model.Context) (model.Context, error) {
	created := cloudContext
	if created.MaxEnvironments <= 0 {
		created.MaxEnvironments = DefaultContextMaxEnvironments
	}
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewInsert().
			Model(&created).
			Column("name", "provider", "cloud_provider_alias", "region", "instance_type", "disk_type", "disk_size_gb", "kubernetes_context", "max_environments").
			Returning("*").
			Scan(ctx)
	})
	return created, err
}

// List returns the tenant's cloud contexts. tenant_id is filtered explicitly
// because erun_operations' RLS policy is unconditional.
func (r *ContextRepository) List(ctx context.Context) ([]model.Context, error) {
	var contexts []model.Context
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		return tx.NewRaw(`
			SELECT `+contextColumns+`
			  FROM contexts
			 WHERE tenant_id = ?
			 ORDER BY name ASC, context_id ASC
		`, securityContext.TenantID).Scan(ctx, &contexts)
	})
	return contexts, err
}

// Get returns the context owned by tenantID; the delete reconciler calls it
// with no tenant in context, so the owner is an explicit predicate.
func (r *ContextRepository) Get(ctx context.Context, tenantID, contextID string) (model.Context, error) {
	var cloudContext model.Context
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT `+contextColumns+`
			  FROM contexts
			 WHERE context_id = ?
			   AND tenant_id = ?
		`, contextID, tenantID).Scan(ctx, &cloudContext)
		return normalizeNoRows(err)
	})
	return cloudContext, err
}

// UpdateProvisioningResult records a provisioning outcome; a failed run clears
// the instance id and IP.
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
