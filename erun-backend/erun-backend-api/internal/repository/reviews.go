package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
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
	created := review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewInsert().
			Model(&created).
			Column("name", "target_branch", "source_branch", "status").
			Returning("*").
			Scan(ctx)
	})
	return created, err
}

func (r *ReviewRepository) Get(ctx context.Context, reviewID string) (model.Review, error) {
	var review model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT `+reviewColumns+`
			  FROM reviews
			 WHERE review_id = ?
		`, reviewID).Scan(ctx, &review)
		return normalizeNoRows(err)
	})
	return review, err
}

func (r *ReviewRepository) List(ctx context.Context, targetBranch string) ([]model.Review, error) {
	var reviews []model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		query := `
			SELECT ` + reviewColumns + `
			  FROM reviews
		`
		var args []any
		if targetBranch != "" {
			query += ` WHERE target_branch = ?`
			args = append(args, targetBranch)
		}
		query += ` ORDER BY created_at DESC, review_id DESC`
		return tx.NewRaw(query, args...).Scan(ctx, &reviews)
	})
	return reviews, err
}

func (r *ReviewRepository) ListMergeQueue(ctx context.Context, targetBranch string) ([]model.Review, error) {
	var reviews []model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		query := `
			SELECT ` + qualifiedReviewColumns + `
			  FROM review_merge_queue q
			  JOIN reviews r
			    ON r.tenant_id = q.tenant_id
			   AND r.target_branch = q.target_branch
			   AND r.review_id = q.review_id
			 WHERE r.status = 'READY'
		`
		var args []any
		if targetBranch != "" {
			query += ` AND q.target_branch = ?`
			args = append(args, targetBranch)
		}
		query += ` ORDER BY q.target_branch ASC, q.review_merge_queue_id ASC`
		return tx.NewRaw(query, args...).Scan(ctx, &reviews)
	})
	return reviews, err
}

func (r *ReviewRepository) Update(ctx context.Context, review model.Review) (model.Review, error) {
	updated := review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewUpdate().
			Model(&updated).
			Column("status", "last_failed_build_id", "last_ready_build_id", "last_merged_build_id").
			Where("review_id = ?", updated.ReviewID).
			Returning("*").
			Scan(ctx)
		return normalizeNoRows(err)
	})
	return updated, err
}

func (r *ReviewRepository) FindNextMergeQueueReview(ctx context.Context, targetBranch string) (model.Review, error) {
	var review model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
		SELECT `+qualifiedReviewColumns+`
		  FROM review_merge_queue q
		  JOIN reviews r
		    ON r.tenant_id = q.tenant_id
		   AND r.target_branch = q.target_branch
		   AND r.review_id = q.review_id
		 WHERE q.target_branch = ?
		   AND r.status = 'READY'
		 ORDER BY q.review_merge_queue_id ASC
		 LIMIT 1
	`, targetBranch).Scan(ctx, &review)
		return normalizeNoRows(err)
	})
	return review, err
}

func (r *ReviewRepository) FindActiveMergeReview(ctx context.Context, targetBranch string) (model.Review, error) {
	var review model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
		SELECT review_id
		     , tenant_id
		     , name
		     , target_branch
		     , source_branch
		     , status
		     , last_failed_build_id
		     , last_ready_build_id
		     , last_merged_build_id
		     , created_at
		     , updated_at
		  FROM reviews
		 WHERE target_branch = ?
		   AND status = 'MERGE'
		 LIMIT 1
	`, targetBranch).Scan(ctx, &review)
		return normalizeNoRows(err)
	})
	return review, err
}

func (r *ReviewRepository) CreateMergeQueueEntry(ctx context.Context, entry model.ReviewMergeQueueEntry) (model.ReviewMergeQueueEntry, error) {
	created := entry
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewInsert().
			Model(&created).
			Column("target_branch", "review_id").
			On("CONFLICT (tenant_id, review_id) DO NOTHING").
			Returning("*").
			Scan(ctx)
	})
	return created, err
}

func (r *ReviewRepository) DeleteMergeQueueEntryByReview(ctx context.Context, reviewID string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			DELETE FROM review_merge_queue
			 WHERE review_id = ?
		`, reviewID).Exec(ctx)
		return err
	})
}
