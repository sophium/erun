package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
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
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
		return err
	})
	return created, err
}

// List returns the caller's tenant's review reviewers, optionally narrowed to
// one review. Scoped explicitly by tenant_id from the security context
// rather than left to RLS: erun_operations' policy is unconditional, so an
// OPERATIONS caller's empty filter would otherwise read every tenant's review
// reviewers.
func (r *ReviewReviewerRepository) List(ctx context.Context, filter ReviewReviewerFilter) ([]model.ReviewReviewer, error) {
	var reviewers []model.ReviewReviewer
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query := `
			SELECT tenant_id, review_id, user_id, created_at, updated_at
			  FROM review_reviewers
			 WHERE tenant_id = ?
		`
		args := []any{securityContext.TenantID}
		if filter.ReviewID != "" {
			query += ` AND review_id = ?`
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
