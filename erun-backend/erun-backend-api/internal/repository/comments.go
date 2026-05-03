package repository

import (
	"context"
	"database/sql"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type CommentRepository struct {
	txs *TxManager
}

func NewCommentRepository(txs *TxManager) *CommentRepository {
	return &CommentRepository{txs: txs}
}

func (r *CommentRepository) Create(ctx context.Context, comment model.Comment) (model.Comment, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Comment{}, ErrMissingSecurityContext
	}
	comment.TenantID = securityContext.TenantID
	if comment.Status == "" {
		comment.Status = model.CommentStatusOpen
	}
	if comment.ParentCommentID == "" {
		comment.CreatorUserID = securityContext.ErunUserID
	}

	var created model.Comment
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, r.txs.rebind(`
			INSERT INTO comments (tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, line)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			RETURNING comment_id, tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, line, created_at, updated_at
		`),
			comment.TenantID,
			comment.ReviewID,
			nullableString(comment.CreatorUserID),
			comment.Status,
			nullableString(comment.ParentCommentID),
			comment.CommitID,
			comment.Line,
		)
		var err error
		created, err = scanComment(row)
		return err
	})
	return created, err
}

func (r *CommentRepository) Get(ctx context.Context, reviewID string, commentID string) (model.Comment, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Comment{}, ErrMissingSecurityContext
	}

	var comment model.Comment
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		comment, err = scanComment(tx.QueryRowContext(ctx, r.txs.rebind(`
			SELECT comment_id, tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, line, created_at, updated_at
			  FROM comments
			 WHERE tenant_id = ?
			   AND review_id = ?
			   AND comment_id = ?
		`), securityContext.TenantID, reviewID, commentID))
		return err
	})
	return comment, err
}

func (r *CommentRepository) ListByReview(ctx context.Context, reviewID string) ([]model.Comment, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return nil, ErrMissingSecurityContext
	}

	var comments []model.Comment
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, r.txs.rebind(`
			SELECT comment_id, tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, line, created_at, updated_at
			  FROM comments
			 WHERE tenant_id = ?
			   AND review_id = ?
			 ORDER BY commit_id, line, created_at, comment_id
		`), securityContext.TenantID, reviewID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			comment, err := scanComment(rows)
			if err != nil {
				return err
			}
			comments = append(comments, comment)
		}
		return rows.Err()
	})
	return comments, err
}

func (r *CommentRepository) UpdateStatus(ctx context.Context, reviewID string, commentID string, status model.CommentStatus) (model.Comment, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Comment{}, ErrMissingSecurityContext
	}

	var comment model.Comment
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		comment, err = scanComment(tx.QueryRowContext(ctx, r.txs.rebind(`
			UPDATE comments
			   SET status = ?
			 WHERE tenant_id = ?
			   AND review_id = ?
			   AND comment_id = ?
			RETURNING comment_id, tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, line, created_at, updated_at
		`), status, securityContext.TenantID, reviewID, commentID))
		return err
	})
	return comment, err
}

func scanComment(row rowScanner) (model.Comment, error) {
	var comment model.Comment
	var creatorUserID sql.NullString
	var parentCommentID sql.NullString
	err := row.Scan(
		&comment.CommentID,
		&comment.TenantID,
		&comment.ReviewID,
		&creatorUserID,
		&comment.Status,
		&parentCommentID,
		&comment.CommitID,
		&comment.Line,
		scanTime(&comment.CreatedAt),
		scanTime(&comment.UpdatedAt),
	)
	if err != nil {
		return model.Comment{}, normalizeNoRows(err)
	}
	comment.CreatorUserID = creatorUserID.String
	comment.ParentCommentID = parentCommentID.String
	return comment, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
