package repository

import (
	"context"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type EnvironmentRepository struct {
	txs *TxManager
}

const environmentColumns = `environment_id, tenant_id, name, type, kubernetes_context, context_id, runtime_version, status, provision_error, deployed_version, expose_error, exposed_hostname, delete_error, delete_attempts, created_at, updated_at`

// environmentsMidTeardownStatuses are the delete-requested statuses Count discounts.
var environmentsMidTeardownStatuses = []string{string(model.EnvironmentStatusDeleting), string(model.EnvironmentStatusDeletionBlocked)}

// maxExcludedMidTeardownEnvironments bounds the discount: wedged teardowns still
// hold live namespaces, so unlimited exclusion would let quota be bypassed.
const maxExcludedMidTeardownEnvironments = 3

func NewEnvironmentRepository(txs *TxManager) *EnvironmentRepository {
	return &EnvironmentRepository{txs: txs}
}

// Create inserts an environment; a context of another tenant is rejected.
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

// List returns the tenant's environments. tenant_id is filtered explicitly
// because erun_operations' RLS policy is unconditional.
func (r *EnvironmentRepository) List(ctx context.Context) ([]model.Environment, error) {
	var environments []model.Environment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		return tx.NewRaw(`
			SELECT `+environmentColumns+`
			  FROM environments
			 WHERE tenant_id = ?
			 ORDER BY name ASC, environment_id ASC
		`, securityContext.TenantID).Scan(ctx, &environments)
	})
	return environments, err
}

// Count returns the tenant's environment count for quota, discounting up to
// maxExcludedMidTeardownEnvironments mid-teardown rows. tenant_id is filtered
// explicitly because erun_operations' RLS policy is unconditional.
func (r *EnvironmentRepository) Count(ctx context.Context) (int, error) {
	var count int
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		return tx.NewRaw(`
			SELECT (SELECT COUNT(*) FROM environments WHERE tenant_id = ?)
			     - LEAST(
			         (SELECT COUNT(*) FROM environments WHERE tenant_id = ? AND status IN (?)),
			         ?
			       )
		`, securityContext.TenantID, securityContext.TenantID, bun.List(environmentsMidTeardownStatuses), maxExcludedMidTeardownEnvironments).Scan(ctx, &count)
	})
	return count, err
}

// CountByContext returns how many of the tenant's environments are on contextID.
func (r *EnvironmentRepository) CountByContext(ctx context.Context, contextID string) (int, error) {
	var count int
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, secErr := security.RequiredFromContext(ctx)
		if secErr != nil {
			return ErrMissingSecurityContext
		}
		var countErr error
		count, countErr = tx.NewSelect().Model((*model.Environment)(nil)).
			Where("context_id = ?", contextID).
			Where("tenant_id = ?", securityContext.TenantID).
			Count(ctx)
		return countErr
	})
	return count, err
}

// CountByType returns how many of the tenant's environments are of envType;
// only runtime environments get a namespace ResourceQuota.
func (r *EnvironmentRepository) CountByType(ctx context.Context, envType model.EnvironmentType) (int, error) {
	var count int
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, secErr := security.RequiredFromContext(ctx)
		if secErr != nil {
			return ErrMissingSecurityContext
		}
		var countErr error
		count, countErr = tx.NewSelect().Model((*model.Environment)(nil)).
			Where("type = ?", string(envType)).
			Where("tenant_id = ?", securityContext.TenantID).
			Count(ctx)
		return countErr
	})
	return count, err
}

// EnvironmentStatusUpdate is one deploy-lifecycle write. An empty
// DeployedVersion or ExposedHostname keeps the recorded value; ProvisionError
// and ExposeError are cleared when empty. ExposeError does not move Status off
// `running`.
type EnvironmentStatusUpdate struct {
	Status          string
	ProvisionError  string
	DeployedVersion string
	ExposeError     string
	ExposedHostname string
}

// UpdateProvisioningStatus persists a provisioning-lifecycle transition.
// tenantID is an explicit predicate because the delete reconciler's context
// carries no tenant id.
func (r *EnvironmentRepository) UpdateProvisioningStatus(ctx context.Context, tenantID, environmentID string, update EnvironmentStatusUpdate) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       provision_error = NULLIF(?, ''),
			       deployed_version = COALESCE(NULLIF(?, ''), deployed_version),
			       expose_error = NULLIF(?, ''),
			       exposed_hostname = COALESCE(NULLIF(?, ''), exposed_hostname)
			 WHERE environment_id = ?
			   AND tenant_id = ?
		`, update.Status, update.ProvisionError, update.DeployedVersion, update.ExposeError, update.ExposedHostname, environmentID, tenantID).Exec(ctx)
		return err
	})
}

// MarkDeployFailed fails a row ClaimDeploy moved to provisioning when a
// synchronous precondition refused the deploy before any workflow ran.
func (r *EnvironmentRepository) MarkDeployFailed(ctx context.Context, tenantID, environmentID, reason string) error {
	return r.UpdateProvisioningStatus(ctx, tenantID, environmentID, EnvironmentStatusUpdate{
		Status:         string(model.EnvironmentStatusFailed),
		ProvisionError: reason,
	})
}

// ClaimDeploy takes the deploy slot, returning false if another deploy holds
// it; a claim older than staleAfter is reclaimable. A row mid-teardown is
// always refused so a deploy cannot overwrite an outstanding delete.
func (r *EnvironmentRepository) ClaimDeploy(ctx context.Context, tenantID, environmentID string, staleAfter time.Duration) (bool, error) {
	claimed := false
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       provision_error = NULL,
			       delete_error = NULL
			 WHERE environment_id = ?
			   AND tenant_id = ?
			   AND status NOT IN (?)
			   AND (status <> ? OR updated_at < NOW() - MAKE_INTERVAL(secs => ?))
		`, string(model.EnvironmentStatusProvisioning), environmentID, tenantID, bun.List(environmentsMidTeardownStatuses), string(model.EnvironmentStatusProvisioning), staleAfter.Seconds()).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		claimed = affected > 0
		return nil
	})
	return claimed, err
}

// Delete hard-deletes an environment row; tenantID is an explicit predicate
// because the delete lifecycle has no tenant in context.
func (r *EnvironmentRepository) Delete(ctx context.Context, tenantID, environmentID string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`DELETE FROM environments WHERE environment_id = ? AND tenant_id = ?`, environmentID, tenantID).Exec(ctx)
		return err
	})
}

// ClaimDelete takes the delete slot, returning false if another delete holds
// it. Stale claims and `deletion-blocked` rows are reclaimable; a fresh
// `provisioning` claim is refused. delete_error is kept until the attempt
// overwrites it, and delete_attempts increments so the reconciler can back off.
func (r *EnvironmentRepository) ClaimDelete(ctx context.Context, tenantID, environmentID string, staleAfter time.Duration) (bool, error) {
	claimed := false
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       delete_attempts = delete_attempts + 1
			 WHERE environment_id = ?
			   AND tenant_id = ?
			   AND (status <> ? OR updated_at < NOW() - MAKE_INTERVAL(secs => ?))
			   AND (status <> ? OR updated_at < NOW() - MAKE_INTERVAL(secs => ?))
		`, string(model.EnvironmentStatusDeleting), environmentID, tenantID, string(model.EnvironmentStatusDeleting), staleAfter.Seconds(), string(model.EnvironmentStatusProvisioning), staleAfter.Seconds()).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		claimed = affected > 0
		return nil
	})
	return claimed, err
}

// MarkDeleteBlocked records why a delete attempt did not tear the namespace
// down, so the row never keeps claiming `running`.
func (r *EnvironmentRepository) MarkDeleteBlocked(ctx context.Context, tenantID, environmentID, reason string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       delete_error = NULLIF(?, '')
			 WHERE environment_id = ?
			   AND tenant_id = ?
		`, string(model.EnvironmentStatusDeletionBlocked), reason, environmentID, tenantID).Exec(ctx)
		return err
	})
}

// ListByStatuses returns environments in any of statuses, across tenants under
// erun_operations; the delete reconciler uses it.
func (r *EnvironmentRepository) ListByStatuses(ctx context.Context, statuses []model.EnvironmentStatus) ([]model.Environment, error) {
	raw := make([]string, len(statuses))
	for i, status := range statuses {
		raw[i] = string(status)
	}
	var environments []model.Environment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT `+environmentColumns+`
			  FROM environments
			 WHERE status IN (?)
			 ORDER BY tenant_id ASC, environment_id ASC
		`, bun.List(raw)).Scan(ctx, &environments)
	})
	return environments, err
}

// Get returns one of the tenant's environments, or ErrNotFound. tenant_id is
// filtered explicitly because erun_operations' RLS policy is unconditional, and
// operate routes treat the returned row as authority to mutate.
func (r *EnvironmentRepository) Get(ctx context.Context, environmentID string) (model.Environment, error) {
	var environment model.Environment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		err = tx.NewRaw(`
			SELECT `+environmentColumns+`
			  FROM environments
			 WHERE tenant_id = ?
			   AND environment_id = ?
		`, securityContext.TenantID, environmentID).Scan(ctx, &environment)
		return normalizeNoRows(err)
	})
	return environment, err
}
