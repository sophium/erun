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

const environmentColumns = `environment_id, tenant_id, name, type, kubernetes_context, context_id, runtime_version, deploy_status, deploy_error, deployed_version, created_at, updated_at`

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

// Count returns how many environments the caller's tenant has. RLS scopes the
// count to the caller's tenant, so no tenant filter is needed here; the quota
// guardrail compares it against the tenant's environment-count cap.
func (r *EnvironmentRepository) Count(ctx context.Context) (int, error) {
	var count int
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		count, err = tx.NewSelect().Model((*model.Environment)(nil)).Count(ctx)
		return err
	})
	return count, err
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

// UpdateDeployResult records the outcome of a runtime-deploy run (issue #680):
// the route flips the env to status="deploying" (empty version/error) when it
// kicks off the durable deploy; on success the executor passes
// status="deployed" with the deployed runtime version and an empty error; on
// failure status="failed" with the reason. RLS scopes the UPDATE to the
// caller's tenant. Empty version/error values normalize to NULL — so a failed
// run clears any stale error only by passing the new one, and a fresh deploy
// run does not leave a half-written version.
// ClaimDeploy atomically claims the environment for a deploy: it flips it to
// deploy_status='deploying' only when it is not already an in-flight deploy, or
// when a prior 'deploying' has gone stale past staleAfter (so a deploy stranded
// by a control-plane crash can be re-deployed instead of being locked forever).
// It returns whether the claim succeeded; false means a deploy is already in
// progress. This makes 'deploying' a concurrency lock rather than a cosmetic
// write, so a double-submit cannot launch a second helm rollout into the same
// release (issue #681 review). RLS scopes the UPDATE to the caller's tenant; the
// timestamp trigger refreshes updated_at, which is what staleAfter measures.
func (r *EnvironmentRepository) ClaimDeploy(ctx context.Context, environmentID string, staleAfter time.Duration) (bool, error) {
	claimed := false
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		res, execErr := tx.NewRaw(`
			UPDATE environments
			   SET deploy_status = 'deploying',
			       deployed_version = NULL,
			       deploy_error = NULL
			 WHERE environment_id = ?
			   AND (deploy_status <> 'deploying' OR updated_at < now() - make_interval(secs => ?))
		`, environmentID, staleAfter.Seconds()).Exec(ctx)
		if execErr != nil {
			return execErr
		}
		affected, raErr := res.RowsAffected()
		if raErr != nil {
			return raErr
		}
		claimed = affected > 0
		return nil
	})
	return claimed, err
}

func (r *EnvironmentRepository) UpdateDeployResult(ctx context.Context, environmentID, status, deployedVersion, deployError string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			UPDATE environments
			   SET deploy_status = ?,
			       deployed_version = NULLIF(?, ''),
			       deploy_error = NULLIF(?, '')
			 WHERE environment_id = ?
		`, status, deployedVersion, deployError, environmentID).Exec(ctx)
		return err
	})
}
