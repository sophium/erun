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

const environmentColumns = `environment_id, tenant_id, name, type, kubernetes_context, context_id, runtime_version, status, provision_error, deployed_version, expose_error, delete_error, delete_attempts, created_at, updated_at`

// environmentsMidTeardownStatuses are the statuses Count excludes: a delete
// has been requested for these rows, so they must not lock a tenant out of
// its own environment allowance through a teardown it cannot complete (#1140)
// — the same "requested a delete" boundary ClaimDelete and MarkDeleteBlocked
// use.
var environmentsMidTeardownStatuses = []string{string(model.EnvironmentStatusDeleting), string(model.EnvironmentStatusDeletionBlocked)}

// maxExcludedMidTeardownEnvironments bounds how many mid-teardown rows Count
// will discount at once. The exclusion exists so one stuck teardown cannot
// lock a tenant out of its own allowance (#1140), but left unbounded it is the
// opposite failure: every wedged environment still holds a live namespace and
// real cluster capacity, so a tenant that accumulates them consumes unlimited
// resource while its quota reports room to spare (#1163). Bounding the
// discount keeps the #1140 property for the case it was written for — a
// handful of stuck deletes — and restores accounting beyond that, where
// "stuck" has become "abandoned".
const maxExcludedMidTeardownEnvironments = 3

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
// the tenant's environment-count quota cap. Environments mid-teardown
// (deleting, deletion-blocked) are discounted, but only up to
// maxExcludedMidTeardownEnvironments: a delete that cannot complete must not
// lock the tenant out of its own allowance (#1140), while an unbounded
// discount would let a tenant hold unlimited live namespaces that its quota
// never sees (#1163). Both subqueries run under the caller's RLS scope, so
// each counts only this tenant's rows.
func (r *EnvironmentRepository) Count(ctx context.Context) (int, error) {
	var count int
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT (SELECT COUNT(*) FROM environments)
			     - LEAST(
			         (SELECT COUNT(*) FROM environments WHERE status IN (?)),
			         ?
			       )
		`, bun.List(environmentsMidTeardownStatuses), maxExcludedMidTeardownEnvironments).Scan(ctx, &count)
	})
	return count, err
}

// CountByContext returns how many environments are already placed on the
// given context, for the placement capacity check (#1112): a context's
// contexts.max_environments names how many it can host.
func (r *EnvironmentRepository) CountByContext(ctx context.Context, contextID string) (int, error) {
	var count int
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		count, err = tx.NewSelect().Model((*model.Environment)(nil)).Where("context_id = ?", contextID).Count(ctx)
		return err
	})
	return count, err
}

// CountByType returns how many of the caller's tenant's environments are of
// the given type, for the aggregate resource-budget check (#1113): only
// runtime environments ever get a namespace ResourceQuota, so the projected
// tenant-wide total is (runtime count + 1) * the per-environment cap.
func (r *EnvironmentRepository) CountByType(ctx context.Context, envType model.EnvironmentType) (int, error) {
	var count int
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		count, err = tx.NewSelect().Model((*model.Environment)(nil)).Where("type = ?", string(envType)).Count(ctx)
		return err
	})
	return count, err
}

// EnvironmentStatusUpdate is one deploy-lifecycle write: the new status, the
// failure reason (cleared on any non-failed state), and the version that
// actually deployed. An empty DeployedVersion leaves the recorded one alone, so
// a failed deploy does not erase the version the cluster is still running.
// ExposeError is distinct from ProvisionError: it names why the deploy Job's
// best-effort chained exposure did not succeed, without moving Status away
// from `running` — the deploy itself landed a healthy workload (#1086).
// Cleared (like ProvisionError) whenever a write carries no expose failure, so
// a later successful expose overwrites a stale one.
type EnvironmentStatusUpdate struct {
	Status          string
	ProvisionError  string
	DeployedVersion string
	ExposeError     string
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
			       deployed_version = COALESCE(NULLIF(?, ''), deployed_version),
			       expose_error = NULLIF(?, '')
			 WHERE environment_id = ?
		`, update.Status, update.ProvisionError, update.DeployedVersion, update.ExposeError, environmentID).Exec(ctx)
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
//
// A row mid-teardown is refused outright, and unlike the in-flight guard this
// refusal is not relaxed by staleAfter: a delete has been requested, so
// adopting the row into a deploy would abandon that request (#1163). Left
// unguarded a deploy claims a `deleting` or `deletion-blocked` row, runs
// against a namespace Kubernetes is already terminating, and writes its own
// `failed` over the teardown state — losing the outstanding delete and
// stranding a row that no longer matches any namespace.
func (r *EnvironmentRepository) ClaimDeploy(ctx context.Context, environmentID string, staleAfter time.Duration) (bool, error) {
	claimed := false
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       provision_error = NULL,
			       delete_error = NULL
			 WHERE environment_id = ?
			   AND status NOT IN (?)
			   AND (status <> ? OR updated_at < NOW() - MAKE_INTERVAL(secs => ?))
		`, string(model.EnvironmentStatusProvisioning), environmentID, bun.List(environmentsMidTeardownStatuses), string(model.EnvironmentStatusProvisioning), staleAfter.Seconds()).Exec(ctx)
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

// ClaimDelete takes exclusive ownership of an environment's delete attempt,
// returning false when another delete already holds it. Mirrors ClaimDeploy:
// a claim that went stale past staleAfter (a control plane that crashed
// mid-delete, or a Job that ran past its own deadline) is re-claimable, so a
// wedged teardown is never locked out permanently — the same property a
// reconciler re-attempting a blocked delete depends on. A row already
// `deletion-blocked` is always reclaimable (not gated by staleAfter): that
// status means the previous attempt already reached a terminal outcome, so
// there is nothing in flight to race with.
//
// A fresh `provisioning` claim is refused, making the two lifecycles mutually
// exclusive in both directions (#1163): claiming a row out from under a
// running deploy leaves that deploy's terminal write to land on top of
// `deleting` and resurrect a row whose namespace is being torn down. The guard
// is relaxed by the same staleAfter as the deploy's own, so a control plane
// that died mid-deploy still cannot block a teardown permanently.
//
// The claim does NOT clear delete_error (#1166). Clearing it on claim meant the
// recorded blocker vanished for as long as the new attempt took to reach the
// same conclusion -- up to NamespaceDeleteTimeout, on a five-minute reconcile
// cycle -- so an operator, the console, or an orchestrator polling during that
// window saw `deleting` with no reason at all for an environment that had been
// stuck for hours. The attempt's own outcome overwrites it instead, so the row
// is never less informative after a tick than it was before.
//
// It does increment delete_attempts, which is what lets the reconciler back off
// per attempt and eventually stop, rather than re-attempting a teardown that
// cannot succeed for as long as the row exists.
func (r *EnvironmentRepository) ClaimDelete(ctx context.Context, environmentID string, staleAfter time.Duration) (bool, error) {
	claimed := false
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       delete_attempts = delete_attempts + 1
			 WHERE environment_id = ?
			   AND (status <> ? OR updated_at < NOW() - MAKE_INTERVAL(secs => ?))
			   AND (status <> ? OR updated_at < NOW() - MAKE_INTERVAL(secs => ?))
		`, string(model.EnvironmentStatusDeleting), environmentID, string(model.EnvironmentStatusDeleting), staleAfter.Seconds(), string(model.EnvironmentStatusProvisioning), staleAfter.Seconds()).Exec(ctx)
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

// MarkDeleteBlocked records a delete attempt that did not tear the namespace
// down, naming why. `running` must not survive a delete attempt (#1140): this
// is the write that keeps a failed or blocked teardown from leaving the row
// exactly where it was, silently claiming to still be up.
func (r *EnvironmentRepository) MarkDeleteBlocked(ctx context.Context, environmentID, reason string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			UPDATE environments
			   SET status = ?,
			       delete_error = NULLIF(?, '')
			 WHERE environment_id = ?
		`, string(model.EnvironmentStatusDeletionBlocked), reason, environmentID).Exec(ctx)
		return err
	})
}

// ListByStatuses returns every environment (across tenants, when run under
// the erun_operations RLS role) whose status is one of the given values. The
// delete reconciler (#1140) uses this, scoped to `deleting`/`deletion-blocked`,
// to find environments whose teardown needs re-attempting without an operator
// noticing and re-issuing the delete.
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
