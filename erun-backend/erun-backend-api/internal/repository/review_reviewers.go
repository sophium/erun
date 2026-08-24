package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type ReviewReviewerRepository struct {
	txs *TxManager
}

type ReviewReviewerFilter struct {
	ReviewID string
}

func NewReviewReviewerRepository(txs *TxManager) *ReviewReviewerRepository {
	return &ReviewReviewerRepository{txs: txs}
}

func (r *ReviewReviewerRepository) Create(ctx context.Context, reviewer model.ReviewReviewer) (model.ReviewReviewer, error) {
	created := reviewer
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewInsert().
			Model(&created).
			Column("review_id", "user_id").
			Returning("*").
			Scan(ctx)
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	})
	return created, err
}

func (r *ReviewReviewerRepository) List(ctx context.Context, filter ReviewReviewerFilter) ([]model.ReviewReviewer, error) {
	var reviewers []model.ReviewReviewer
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		query := `
			SELECT tenant_id, review_id, user_id, created_at, updated_at
			  FROM review_reviewers
		`
		var args []any
		if filter.ReviewID != "" {
			query += ` WHERE review_id = ?`
			args = append(args, filter.ReviewID)
		}
		query += ` ORDER BY created_at, user_id`
		return tx.NewRaw(query, args...).Scan(ctx, &reviewers)
	})
	return reviewers, err
}

func (r *ReviewReviewerRepository) Delete(ctx context.Context, reviewID, userID string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`
			DELETE FROM review_reviewers
			 WHERE review_id = ?
			   AND user_id = ?
		`, reviewID, userID).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrNotFound
		}
		return nil
	})
}
