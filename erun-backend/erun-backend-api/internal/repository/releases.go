package repository

import (
	"context"
	"errors"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type ReleaseRepository struct {
	txs *TxManager
}

const releaseColumns = `release_id, tenant_id, review_id, target_branch, commit_id, status, attempt, version, build_id, failure_reason, created_at, updated_at`

func NewReleaseRepository(txs *TxManager) *ReleaseRepository {
	return &ReleaseRepository{txs: txs}
}

// ReleaseFilter narrows a release listing.
type ReleaseFilter struct {
	ReviewID     string
	TargetBranch string
	Status       model.ReleaseStatus
}

// Create enqueues a release; status, attempt and executor fields are database-owned.
func (r *ReleaseRepository) Create(ctx context.Context, release model.Release) (model.Release, error) {
	created := release
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewInsert().
			Model(&created).
			Column("review_id", "target_branch", "commit_id").
			Returning("*").
			Scan(ctx)
	})
	return created, err
}

func (r *ReleaseRepository) Get(ctx context.Context, releaseID string) (model.Release, error) {
	var release model.Release
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT `+releaseColumns+`
			  FROM releases
			 WHERE release_id = ?
		`, releaseID).Scan(ctx, &release)
		return normalizeNoRows(err)
	})
	return release, err
}

// FindByCommit resolves the release already recorded for a merge commit.
func (r *ReleaseRepository) FindByCommit(ctx context.Context, commitID string) (model.Release, error) {
	var release model.Release
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT `+releaseColumns+`
			  FROM releases
			 WHERE commit_id = ?
		`, commitID).Scan(ctx, &release)
		return normalizeNoRows(err)
	})
	return release, err
}

// List returns the caller's tenant's releases, scoped explicitly by tenant_id
// because erun_operations' RLS policy is unconditional.
func (r *ReleaseRepository) List(ctx context.Context, filter ReleaseFilter) ([]model.Release, error) {
	var releases []model.Release
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query := `SELECT ` + releaseColumns + ` FROM releases WHERE tenant_id = ?`
		args := []any{securityContext.TenantID}
		if filter.ReviewID != "" {
			query += ` AND review_id = ?`
			args = append(args, filter.ReviewID)
		}
		if filter.TargetBranch != "" {
			query += ` AND target_branch = ?`
			args = append(args, filter.TargetBranch)
		}
		if filter.Status != "" {
			query += ` AND status = ?`
			args = append(args, string(filter.Status))
		}
		query += ` ORDER BY release_id DESC`
		return tx.NewRaw(query, args...).Scan(ctx, &releases)
	})
	return releases, err
}

// ClaimWindow bounds when the serial queue hands out the next release. Cooldown
// spaces a tenant's releases so a looping trigger cannot run them back to back.
type ClaimWindow struct {
	Cooldown time.Duration
}

// ClaimNext marks the tenant's oldest queued release running, or returns false.
// The partial unique index on running rows enforces serialisation, so a
// conflict means another claimer won. The cooldown check filters tenant_id
// explicitly because the operations role sees every tenant.
func (r *ReleaseRepository) ClaimNext(ctx context.Context, window ClaimWindow) (model.Release, bool, error) {
	var release model.Release
	claimed := false
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			UPDATE releases
			   SET status = 'running',
			       failure_reason = NULL
			 WHERE release_id = (
			   SELECT candidate.release_id
			     FROM releases candidate
			    WHERE candidate.status = 'queued'
			      AND NOT EXISTS (
			        SELECT 1
			          FROM releases inflight
			         WHERE inflight.tenant_id = candidate.tenant_id
			           AND inflight.status = 'running'
			      )
			      AND NOT EXISTS (
			        SELECT 1
			          FROM releases recent
			         WHERE recent.tenant_id = candidate.tenant_id
			           AND recent.status IN ('released', 'failed')
			           AND recent.updated_at > NOW() - MAKE_INTERVAL(secs => ?)
			      )
			    ORDER BY candidate.release_id ASC
			    LIMIT 1
			 )
			RETURNING `+releaseColumns+`
		`, window.Cooldown.Seconds()).Scan(ctx, &release)
		switch {
		case err == nil:
			claimed = true
			return nil
		case errors.Is(normalizeNoRows(err), ErrNotFound), isUniqueViolation(err):
			return nil
		default:
			return err
		}
	})
	return release, claimed, err
}

// ExpireStale fails releases running past staleAfter so a dead control plane
// cannot hold the tenant's in-flight slot.
func (r *ReleaseRepository) ExpireStale(ctx context.Context, staleAfter time.Duration, reason string) (int, error) {
	expired := 0
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`
			UPDATE releases
			   SET status = 'failed',
			       failure_reason = ?
			 WHERE status = 'running'
			   AND updated_at < NOW() - MAKE_INTERVAL(secs => ?)
		`, reason, staleAfter.Seconds()).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		expired = int(affected)
		return nil
	})
	return expired, err
}

// Requeue returns a terminal release to the queue with a bumped attempt. A
// published release is never requeued: its version is already public.
func (r *ReleaseRepository) Requeue(ctx context.Context, releaseID string) (model.Release, error) {
	var release model.Release
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			UPDATE releases
			   SET status = 'queued',
			       attempt = attempt + 1,
			       failure_reason = NULL
			 WHERE release_id = ?
			   AND status = 'failed'
			RETURNING `+releaseColumns+`
		`, releaseID).Scan(ctx, &release)
		return normalizeNoRows(err)
	})
	return release, err
}

// ReleaseOutcome is one terminal write; Version and BuildID are set only on success.
type ReleaseOutcome struct {
	Status        model.ReleaseStatus
	Version       string
	BuildID       string
	FailureReason string
}

// RecordOutcome persists a release attempt's terminal state. The version is
// written only here, after publish, so a failed run never leaves a phantom one.
func (r *ReleaseRepository) RecordOutcome(ctx context.Context, releaseID string, outcome ReleaseOutcome) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			UPDATE releases
			   SET status = ?,
			       version = COALESCE(NULLIF(?, ''), version),
			       build_id = COALESCE(NULLIF(?, '')::uuid, build_id),
			       failure_reason = NULLIF(?, '')
			 WHERE release_id = ?
		`, string(outcome.Status), outcome.Version, outcome.BuildID, outcome.FailureReason, releaseID).Exec(ctx)
		return err
	})
}
