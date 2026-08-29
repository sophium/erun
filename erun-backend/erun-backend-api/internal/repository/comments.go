package repository

import (
	"context"

	"github.com/jackc/pgerrcode"
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

const commentColumns = `comment_id, tenant_id, review_id, creator_user_id, status, parent_comment_id, commit_id, file_path, line, body, created_at, updated_at`

func (r *CommentRepository) Create(ctx context.Context, comment model.Comment) (model.Comment, error) {
	created := comment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewInsert().
			Model(&created).
			Column("review_id", "creator_user_id", "status", "parent_comment_id", "commit_id", "file_path", "line", "body").
			Returning("*").
			Scan(ctx)
		return classifyCommentError(err)
	})
	return created, err
}

func (r *CommentRepository) Get(ctx context.Context, commentID string) (model.Comment, error) {
	var comment model.Comment
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT `+commentColumns+`
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
			SELECT ` + commentColumns + `
			  FROM comments
		`
		var args []any
		if filter.ReviewID != "" {
			query += ` WHERE review_id = ?`
			args = append(args, filter.ReviewID)
		}
		query += ` ORDER BY commit_id, file_path, line, created_at, comment_id`
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
			RETURNING `+commentColumns+`
		`, updated.Status, updated.CommentID).Scan(ctx, &updated)
		return classifyCommentError(normalizeNoRows(err))
	})
	return updated, err
}

// classifyCommentError maps the comments table's foreign keys and CHECK
// constraints, and the erun_validate_comments trigger's RAISE EXCEPTIONs,
// onto the repository's sentinel errors so callers see a 4xx instead of a
// bare 500 — a reviewId/parentCommentId the caller's tenant cannot see fails
// the same foreign key check whether the row genuinely doesn't exist or just
// isn't this tenant's, matching the "doesn't exist or isn't visible" 404
// documented in collaboration/comments.md.
func classifyCommentError(err error) error {
	code, ok := pgErrorCode(err)
	if !ok {
		return err
	}
	if code == pgerrcode.CheckViolation {
		if name, ok := pgConstraintName(err); ok && name == "comments_body_check" {
			return ErrCommentBodyInvalid
		}
	}
	switch code {
	case pgerrcode.ForeignKeyViolation:
		return ErrNotFound
	case pgerrcode.NotNullViolation, pgerrcode.CheckViolation:
		return ErrInvalidInput
	case pgerrcode.UniqueViolation:
		return ErrConflict
	case pgerrcode.InsufficientPrivilege:
		return ErrForbidden
	default:
		return err
	}
}
