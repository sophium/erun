package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type CommentRepository struct {
	txs *TxManager
}

type CommentFilter struct {
	ReviewID string
}

func NewCommentRepository(txs *TxManager) *CommentRepository {
	return &CommentRepository{txs: txs}
}

func (r *CommentRepository) Create(ctx context.Context, comment model.Comment) (model.Comment, error) {
	created := comment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewInsert().
			Model(&created).
			Column("review_id", "creator_user_id", "status", "parent_comment_id", "commit_id", "line").
			Returning("*").
			Scan(ctx)
	})
	return created, err
}

func (r *CommentRepository) Get(ctx context.Context, commentID string) (model.Comment, error) {
	var comment model.Comment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT comment_id, tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, line, created_at, updated_at
			  FROM comments
			 WHERE comment_id = ?
		`, commentID).Scan(ctx, &comment)
		return normalizeNoRows(err)
	})
	return comment, err
}

func (r *CommentRepository) List(ctx context.Context, filter CommentFilter) ([]model.Comment, error) {
	var comments []model.Comment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		query := `
			SELECT comment_id, tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, line, created_at, updated_at
			  FROM comments
		`
		var args []any
		if filter.ReviewID != "" {
			query += ` WHERE review_id = ?`
			args = append(args, filter.ReviewID)
		}
		query += ` ORDER BY commit_id, line, created_at, comment_id`
		return tx.NewRaw(query, args...).Scan(ctx, &comments)
	})
	return comments, err
}

func (r *CommentRepository) Update(ctx context.Context, comment model.Comment) (model.Comment, error) {
	updated := comment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			UPDATE comments
			   SET status = ?
			 WHERE comment_id = ?
			RETURNING comment_id, tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, line, created_at, updated_at
		`, updated.Status, updated.CommentID).Scan(ctx, &updated)
		return normalizeNoRows(err)
	})
	return updated, err
}
