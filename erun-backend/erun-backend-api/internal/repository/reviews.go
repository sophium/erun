package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type ReviewRepository struct {
	txs *TxManager
}

const reviewColumns = `review_id, tenant_id, name, target_branch, source_branch, status, last_failed_build_id, last_ready_build_id, last_merged_build_id, created_at, updated_at`
const qualifiedReviewColumns = `r.review_id, r.tenant_id, r.name, r.target_branch, r.source_branch, r.status, r.last_failed_build_id, r.last_ready_build_id, r.last_merged_build_id, r.created_at, r.updated_at`

func NewReviewRepository(txs *TxManager) *ReviewRepository {
	return &ReviewRepository{txs: txs}
}

func (r *ReviewRepository) Create(ctx context.Context, review model.Review) (model.Review, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Review{}, ErrMissingSecurityContext
	}
	review.TenantID = securityContext.TenantID
	if review.Status == "" {
		review.Status = model.ReviewStatusOpen
	}
	if review.ReviewID == "" {
		reviewID, err := newUUIDv7()
		if err != nil {
			return model.Review{}, err
		}
		review.ReviewID = reviewID
	}

	var created model.Review
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, r.txs.rebind(`
			INSERT INTO reviews (review_id, tenant_id, name, target_branch, source_branch, status)
			VALUES (?, ?, ?, ?, ?, ?)
			RETURNING `+reviewColumns+`
		`), review.ReviewID, review.TenantID, review.Name, review.TargetBranch, review.SourceBranch, review.Status)
		var err error
		created, err = scanReview(row)
		return err
	})
	return created, err
}

func (r *ReviewRepository) Get(ctx context.Context, reviewID string) (model.Review, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Review{}, ErrMissingSecurityContext
	}

	var review model.Review
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		review, err = scanReview(tx.QueryRowContext(ctx, r.txs.rebind(`
			SELECT `+reviewColumns+`
			  FROM reviews
			 WHERE tenant_id = ?
			   AND review_id = ?
		`), securityContext.TenantID, reviewID))
		return err
	})
	return review, err
}

func (r *ReviewRepository) List(ctx context.Context, targetBranch string) ([]model.Review, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return nil, ErrMissingSecurityContext
	}

	var reviews []model.Review
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		query := `
			SELECT ` + reviewColumns + `
			  FROM reviews
			 WHERE tenant_id = ?
		`
		args := []any{securityContext.TenantID}
		if targetBranch != "" {
			query += ` AND target_branch = ?`
			args = append(args, targetBranch)
		}
		query += ` ORDER BY created_at DESC, review_id DESC`
		rows, err := tx.QueryContext(ctx, r.txs.rebind(query), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			review, err := scanReview(rows)
			if err != nil {
				return err
			}
			reviews = append(reviews, review)
		}
		return rows.Err()
	})
	return reviews, err
}

func (r *ReviewRepository) ListMergeQueue(ctx context.Context, targetBranch string) ([]model.Review, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return nil, ErrMissingSecurityContext
	}
	if targetBranch == "" {
		return nil, ErrInvalidInput
	}

	var reviews []model.Review
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, r.txs.rebind(`
			SELECT `+qualifiedReviewColumns+`
			  FROM review_merge_queue q
			  JOIN reviews r
			    ON r.tenant_id = q.tenant_id
			   AND r.target_branch = q.target_branch
			   AND r.review_id = q.review_id
			 WHERE q.tenant_id = ?
			   AND q.target_branch = ?
			   AND r.status = 'READY'
			 ORDER BY q.review_merge_queue_id ASC
		`), securityContext.TenantID, targetBranch)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			review, err := scanReview(rows)
			if err != nil {
				return err
			}
			reviews = append(reviews, review)
		}
		return rows.Err()
	})
	return reviews, err
}

func (r *ReviewRepository) AdvanceMergeQueue(ctx context.Context, targetBranch string) (model.Review, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Review{}, ErrMissingSecurityContext
	}
	if targetBranch == "" {
		return model.Review{}, ErrInvalidInput
	}

	var review model.Review
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		review, err = r.promoteNextMerge(ctx, tx, securityContext.TenantID, targetBranch)
		return err
	})
	return review, err
}

func (r *ReviewRepository) UpdateStatus(ctx context.Context, reviewID string, status model.ReviewStatus, buildID string) (model.Review, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Review{}, ErrMissingSecurityContext
	}

	var review model.Review
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if status == model.ReviewStatusMerge {
			return ErrInvalidInput
		}
		column := reviewLastBuildColumn(status)
		var err error
		if column == "" {
			review, err = scanReview(tx.QueryRowContext(ctx, r.txs.rebind(`
				UPDATE reviews
				   SET status = ?
				 WHERE tenant_id = ?
				   AND review_id = ?
				RETURNING `+reviewColumns+`
			`), status, securityContext.TenantID, reviewID))
			if err != nil {
				return err
			}
			return r.dequeueReview(ctx, tx, securityContext.TenantID, reviewID)
		}
		if status == model.ReviewStatusReady && buildID == "" {
			review, err = scanReview(tx.QueryRowContext(ctx, r.txs.rebind(`
				UPDATE reviews
				   SET status = 'READY'
				 WHERE tenant_id = ?
				   AND review_id = ?
				   AND status = 'MERGE'
				RETURNING `+reviewColumns+`
			`), securityContext.TenantID, reviewID))
			if err != nil {
				return err
			}
			return r.enqueueReview(ctx, tx, securityContext.TenantID, reviewID)
		}
		review, err = r.updateBuildStatus(ctx, tx, securityContext.TenantID, reviewID, status, buildID, column)
		return err
	})
	return review, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanReview(row rowScanner) (model.Review, error) {
	var review model.Review
	var lastFailedBuildID sql.NullString
	var lastReadyBuildID sql.NullString
	var lastMergedBuildID sql.NullString
	err := row.Scan(
		&review.ReviewID,
		&review.TenantID,
		&review.Name,
		&review.TargetBranch,
		&review.SourceBranch,
		&review.Status,
		&lastFailedBuildID,
		&lastReadyBuildID,
		&lastMergedBuildID,
		&review.CreatedAt,
		&review.UpdatedAt,
	)
	if err != nil {
		return model.Review{}, normalizeNoRows(err)
	}
	review.LastFailedBuildID = lastFailedBuildID.String
	review.LastReadyBuildID = lastReadyBuildID.String
	review.LastMergedBuildID = lastMergedBuildID.String
	return review, nil
}

func reviewLastBuildColumn(status model.ReviewStatus) string {
	switch status {
	case model.ReviewStatusFailed:
		return "last_failed_build_id"
	case model.ReviewStatusReady:
		return "last_ready_build_id"
	case model.ReviewStatusMerged:
		return "last_merged_build_id"
	default:
		return ""
	}
}

func (r *ReviewRepository) updateBuildStatus(ctx context.Context, tx *sql.Tx, tenantID string, reviewID string, status model.ReviewStatus, buildID string, column string) (model.Review, error) {
	if buildID == "" {
		return model.Review{}, ErrInvalidInput
	}
	expectedSuccessful := status != model.ReviewStatusFailed
	query := `
		UPDATE reviews
		   SET status = ?,
		       ` + column + ` = ?
		 WHERE tenant_id = ?
		   AND review_id = ?
		   AND EXISTS (
		     SELECT 1
		       FROM builds
		      WHERE builds.tenant_id = reviews.tenant_id
		        AND builds.review_id = reviews.review_id
		        AND builds.build_id = ?
		        AND builds.successful = ?
		   )
		RETURNING ` + reviewColumns
	review, err := scanReview(tx.QueryRowContext(ctx, r.txs.rebind(query), status, buildID, tenantID, reviewID, buildID, expectedSuccessful))
	if err != nil {
		return model.Review{}, err
	}
	if status == model.ReviewStatusReady {
		return review, r.enqueueReview(ctx, tx, tenantID, reviewID)
	}
	return review, r.dequeueReview(ctx, tx, tenantID, reviewID)
}

func (r *ReviewRepository) markBuildResult(ctx context.Context, tx *sql.Tx, tenantID string, reviewID string, buildID string, successful bool) error {
	targetBranch, err := r.targetBranchForReview(ctx, tx, tenantID, reviewID)
	if err != nil {
		return err
	}
	status := model.ReviewStatusFailed
	column := "last_failed_build_id"
	if successful {
		status = model.ReviewStatusReady
		column = "last_ready_build_id"
	}
	eligibleStatuses := "'OPEN', 'FAILED', 'READY', 'MERGE'"
	if successful {
		eligibleStatuses = "'OPEN', 'FAILED'"
	}
	query := `
		UPDATE reviews
		   SET status = ?,
		       ` + column + ` = ?
		 WHERE tenant_id = ?
		   AND review_id = ?
		   AND status IN (` + eligibleStatuses + `)
	`
	result, err := tx.ExecContext(ctx, r.txs.rebind(query), status, buildID, tenantID, reviewID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	if successful {
		if err := r.enqueueReview(ctx, tx, tenantID, reviewID); err != nil {
			return err
		}
		_, err = r.promoteNextMerge(ctx, tx, tenantID, targetBranch)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return nil
	}
	return r.dequeueReview(ctx, tx, tenantID, reviewID)
}

func (r *ReviewRepository) promoteNextMerge(ctx context.Context, tx *sql.Tx, tenantID string, targetBranch string) (model.Review, error) {
	if err := r.requireNoActiveMerge(ctx, tx, tenantID, targetBranch); err != nil {
		return model.Review{}, err
	}

	var reviewID string
	err := tx.QueryRowContext(ctx, r.txs.rebind(`
		SELECT q.review_id
		  FROM review_merge_queue q
		  JOIN reviews r
		    ON r.tenant_id = q.tenant_id
		   AND r.target_branch = q.target_branch
		   AND r.review_id = q.review_id
		 WHERE q.tenant_id = ?
		   AND q.target_branch = ?
		   AND r.status = 'READY'
		 ORDER BY q.review_merge_queue_id ASC
		 LIMIT 1
	`), tenantID, targetBranch).Scan(&reviewID)
	if err != nil {
		return model.Review{}, normalizeNoRows(err)
	}
	if err := r.dequeueReview(ctx, tx, tenantID, reviewID); err != nil {
		return model.Review{}, err
	}
	return scanReview(tx.QueryRowContext(ctx, r.txs.rebind(`
		UPDATE reviews
		   SET status = 'MERGE'
		 WHERE tenant_id = ?
		   AND review_id = ?
		   AND status = 'READY'
		RETURNING `+reviewColumns+`
	`), tenantID, reviewID))
}

func (r *ReviewRepository) targetBranchForReview(ctx context.Context, tx *sql.Tx, tenantID string, reviewID string) (string, error) {
	var targetBranch string
	err := tx.QueryRowContext(ctx, r.txs.rebind(`
		SELECT target_branch
		  FROM reviews
		 WHERE tenant_id = ?
		   AND review_id = ?
	`), tenantID, reviewID).Scan(&targetBranch)
	if err != nil {
		return "", normalizeNoRows(err)
	}
	return targetBranch, nil
}

func (r *ReviewRepository) enqueueReview(ctx context.Context, tx *sql.Tx, tenantID string, reviewID string) error {
	targetBranch, err := r.targetBranchForReview(ctx, tx, tenantID, reviewID)
	if err != nil {
		return err
	}
	if err := r.dequeueReview(ctx, tx, tenantID, reviewID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, r.txs.rebind(`
		INSERT INTO review_merge_queue (tenant_id, target_branch, review_id)
		VALUES (?, ?, ?)
		ON CONFLICT (tenant_id, review_id) DO NOTHING
	`), tenantID, targetBranch, reviewID)
	return err
}

func (r *ReviewRepository) dequeueReview(ctx context.Context, tx *sql.Tx, tenantID string, reviewID string) error {
	_, err := tx.ExecContext(ctx, r.txs.rebind(`
		DELETE FROM review_merge_queue
		 WHERE tenant_id = ?
		   AND review_id = ?
	`), tenantID, reviewID)
	return err
}

func (r *ReviewRepository) requireNoActiveMerge(ctx context.Context, tx *sql.Tx, tenantID string, targetBranch string) error {
	var activeReviewID string
	err := tx.QueryRowContext(ctx, r.txs.rebind(`
		SELECT review_id
		  FROM reviews
		 WHERE tenant_id = ?
		   AND target_branch = ?
		   AND status = 'MERGE'
		 LIMIT 1
	`), tenantID, targetBranch).Scan(&activeReviewID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrNotFound
}
