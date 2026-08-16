package repository

import (
	"context"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type ReleaseRepository struct {
	txs *TxManager
}

const releaseColumns = `release_id, tenant_id, review_id, target_branch, commit_id, status, attempt, version, build_id, failure_reason, created_at, updated_at`

func NewReleaseRepository(txs *TxManager) *ReleaseRepository {
	return &ReleaseRepository{txs: txs}
}

// ReleaseFilter narrows a release listing. An empty filter lists the tenant's
// whole queue and history, newest first.
type ReleaseFilter struct {
	ReviewID     string
	TargetBranch string
	Status       model.ReleaseStatus
}

// Create enqueues a release. The status, attempt and every executor-owned field
// are database-owned, so a caller cannot enqueue a row that claims to have
// already released.
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

// FindByCommit resolves the release already recorded for a merge commit. It is
// how the trigger answers "this commit is already released" without inserting.
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

func (r *ReleaseRepository) List(ctx context.Context, filter ReleaseFilter) ([]model.Release, error) {
	var releases []model.Release
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		query := `SELECT ` + releaseColumns + ` FROM releases WHERE TRUE`
		var args []any
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

// ClaimWindow bounds when the serial queue will hand out the next release.
// Cooldown is the minimum spacing between consecutive releases for one tenant,
// so a trigger stuck in a loop cannot spend the cluster on back-to-back releases.
type ClaimWindow struct {
	Cooldown time.Duration
}

// ClaimNext takes the tenant's oldest queued release and marks it running,
// returning false when the tenant already has one in flight, is inside its
// cooldown, or has nothing queued. Ordering by the UUIDv7 release_id makes the
// queue FIFO by enqueue time.
//
// The serialisation invariant itself is the partial unique index on running
// rows, not this predicate: two claimers racing would both read the same
// pre-claim snapshot, so the loser has to lose in the database. A conflict is
// therefore "somebody else claimed it", not an error.
//
// The cooldown check correlates on tenant_id rather than relying on row-level
// security to scope it, because the window is per tenant even under the
// operations role, whose policy sees every tenant's rows.
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
		case normalizeNoRows(err) == ErrNotFound, isUniqueViolation(err):
			return nil
		default:
			return err
		}
	})
	return release, claimed, err
}

// ExpireStale fails any release still marked running past staleAfter, so a
// control plane that died mid-release does not hold its tenant's only in-flight
// slot forever. The reason it records says the run stopped reporting rather than
// claiming the release itself failed, because what actually happened to the Job
// is unknown from here.
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

// Requeue returns a terminal release to the queue as a fresh attempt. The bumped
// attempt is what keys the next Job and durable workflow, so the retry runs
// instead of replaying the previous attempt's outcome. A release that already
// published is never requeued: its version is public, and a second one for the
// same commit is the failure this queue exists to prevent.
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

// ReleaseOutcome is one terminal write for a release attempt: the status, the
// version it published, the build it recorded, and the reason it failed. Version
// and BuildID are only ever set on a successful publish.
type ReleaseOutcome struct {
	Status        model.ReleaseStatus
	Version       string
	BuildID       string
	FailureReason string
}

// RecordOutcome persists a release attempt's terminal state. The version is
// written only here, after the run published it, because a version minted
// locally by a run that then failed is a phantom nothing can resolve.
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
