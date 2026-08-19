package repository

import (
	"context"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type EnvironmentRepository struct {
	txs *TxManager
}

const environmentColumns = `environment_id, tenant_id, name, type, kubernetes_context, context_id, runtime_version, status, provision_error, deployed_version, created_at, updated_at`

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

// EnvironmentStatusUpdate is one deploy-lifecycle write: the new status, the
// failure reason (cleared on any non-failed state), and the version that
// actually deployed. An empty DeployedVersion leaves the recorded one alone, so
// a failed deploy does not erase the version the cluster is still running.
type EnvironmentStatusUpdate struct {
	Status          string
	ProvisionError  string
	DeployedVersion string
}

// UpdateProvisioningStatus persists an environment's provisioning-lifecycle
// transition (registered → provisioning → running/failed). Mirrors the contexts
// provisioning-result update; RLS keeps the write scoped to the caller's tenant.
func (r *EnvironmentRepository) UpdateProvisioningStatus(ctx context.Context, environmentID string, update EnvironmentStatusUpdate) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       provision_error = NULLIF(?, ''),
			       deployed_version = COALESCE(NULLIF(?, ''), deployed_version)
			 WHERE environment_id = ?
		`, update.Status, update.ProvisionError, update.DeployedVersion, environmentID).Exec(ctx)
		return err
	})
}

// MarkDeployFailed records a deploy that never made it to the durable
// workflow — a synchronous precondition (e.g. the tenant's runtime image is
// confirmed missing) refused it after ClaimDeploy already moved the row to
// provisioning. Without this the environment would be stranded in
// provisioning forever, since no workflow run exists to mark it failed.
func (r *EnvironmentRepository) MarkDeployFailed(ctx context.Context, environmentID, reason string) error {
	return r.UpdateProvisioningStatus(ctx, environmentID, EnvironmentStatusUpdate{
		Status:         string(model.EnvironmentStatusFailed),
		ProvisionError: reason,
	})
}

// ClaimDeploy takes exclusive ownership of an environment's deploy slot,
// returning false when another deploy already holds it. This is what makes a
// double-submit safe: two concurrent requests would otherwise both launch a
// rollout into the same release, and the loser's terminal write could clobber
// the winner's. A claim that went stale past staleAfter -- a control plane that
// crashed mid-deploy -- is re-claimable, so a wedged env is never locked out
// permanently.
func (r *EnvironmentRepository) ClaimDeploy(ctx context.Context, environmentID string, staleAfter time.Duration) (bool, error) {
	claimed := false
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       provision_error = NULL
			 WHERE environment_id = ?
			   AND (status <> ? OR updated_at < NOW() - MAKE_INTERVAL(secs => ?))
		`, string(model.EnvironmentStatusProvisioning), environmentID, string(model.EnvironmentStatusProvisioning), staleAfter.Seconds()).Exec(ctx)
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

// Delete hard-deletes an environment row, once its namespace (if any) has
// been torn down. RLS keeps the delete scoped to the caller's tenant.
func (r *EnvironmentRepository) Delete(ctx context.Context, environmentID string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`DELETE FROM environments WHERE environment_id = ?`, environmentID).Exec(ctx)
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
