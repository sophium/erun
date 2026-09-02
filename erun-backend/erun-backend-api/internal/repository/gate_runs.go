package repository

import (
	"context"

	"github.com/jackc/pgerrcode"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type GateRunRepository struct {
	txs *TxManager
}

// GateRunFilter composes GET /v1/gate-runs discovery filters. Every field is
// optional; an empty filter lists every gate run visible to the caller's
// tenant.
type GateRunFilter struct {
	TargetBranch string
	SourceBranch string
	Status       model.GateRunStatus
}

func NewGateRunRepository(txs *TxManager) *GateRunRepository {
	return &GateRunRepository{txs: txs}
}

func (r *GateRunRepository) Create(ctx context.Context, run model.GateRun) (model.GateRun, error) {
	created := run
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewInsert().
			Model(&created).
			Column("source_branch", "target_branch", "source_commit", "merge_commit", "review_id", "status", "failing_step", "log_ref").
			Returning("*").
			Scan(ctx)
		return classifyGateRunError(err)
	})
	return created, err
}

// classifyGateRunError maps gate_runs' foreign key and CHECK constraints onto
// the repository's sentinel errors, mirroring classifyBuildError: a reviewId
// the caller's tenant cannot see fails the same foreign key check whether it
// genuinely doesn't exist or just isn't this tenant's.
func classifyGateRunError(err error) error {
	code, ok := pgErrorCode(err)
	if !ok {
		return err
	}
	switch code {
	case pgerrcode.ForeignKeyViolation:
		return ErrNotFound
	case pgerrcode.NotNullViolation, pgerrcode.CheckViolation:
		return ErrInvalidInput
	default:
		return err
	}
}

func (r *GateRunRepository) Get(ctx context.Context, gateRunID string) (model.GateRun, error) {
	var run model.GateRun
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT g.gate_run_id, g.tenant_id, g.source_branch, g.target_branch, g.source_commit,
			       g.merge_commit, g.review_id, g.status, g.failing_step, g.log_ref,
			       g.created_at, g.updated_at, r.name AS review_name
			  FROM gate_runs g
			  LEFT JOIN reviews r
			    ON r.tenant_id = g.tenant_id
			   AND r.review_id = g.review_id
			 WHERE g.gate_run_id = ?
		`, gateRunID).Scan(ctx, &run)
		return normalizeNoRows(err)
	})
	return run, err
}

// List returns the caller's tenant's gate runs, most recent first, narrowed
// by filter. Scoped explicitly by tenant_id from the security context rather
// than left to RLS: erun_operations' policy is unconditional, so an
// OPERATIONS caller's empty filter would otherwise read every tenant's gate
// runs.
func (r *GateRunRepository) List(ctx context.Context, filter GateRunFilter) ([]model.GateRun, error) {
	var runs []model.GateRun
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query := `
			SELECT g.gate_run_id, g.tenant_id, g.source_branch, g.target_branch, g.source_commit,
			       g.merge_commit, g.review_id, g.status, g.failing_step, g.log_ref,
			       g.created_at, g.updated_at, r.name AS review_name
			  FROM gate_runs g
			  LEFT JOIN reviews r
			    ON r.tenant_id = g.tenant_id
			   AND r.review_id = g.review_id
			 WHERE g.tenant_id = ?
		`
		args := []any{securityContext.TenantID}
		if filter.TargetBranch != "" {
			query += ` AND g.target_branch = ?`
			args = append(args, filter.TargetBranch)
		}
		if filter.SourceBranch != "" {
			query += ` AND g.source_branch = ?`
			args = append(args, filter.SourceBranch)
		}
		if filter.Status != "" {
			query += ` AND g.status = ?`
			args = append(args, filter.Status)
		}
		query += ` ORDER BY g.created_at DESC, g.gate_run_id DESC`
		return tx.NewRaw(query, args...).Scan(ctx, &runs)
	})
	return runs, err
}

// Update persists a gate run's reported outcome: status, failing_step,
// log_ref, and merge_commit (set here rather than at Create when the caller
// only learns it once the squash-merge succeeds). Every other field is
// immutable after creation.
func (r *GateRunRepository) Update(ctx context.Context, run model.GateRun) (model.GateRun, error) {
	updated := run
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewUpdate().
			Model(&updated).
			Column("status", "failing_step", "log_ref", "merge_commit").
			Where("gate_run_id = ?", updated.GateRunID).
			Returning("*").
			Scan(ctx)
		return classifyGateRunError(normalizeNoRows(err))
	})
	return updated, err
}
