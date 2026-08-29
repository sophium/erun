package repository

import (
	"context"

	"github.com/jackc/pgerrcode"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type BuildRepository struct {
	txs *TxManager
}

type BuildFilter struct {
	ReviewID string
}

func NewBuildRepository(txs *TxManager) *BuildRepository {
	return &BuildRepository{txs: txs}
}

func (r *BuildRepository) Create(ctx context.Context, build model.Build) (model.Build, error) {
	created := build
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewInsert().
			Model(&created).
			Column("review_id", "kind", "successful", "commit_id", "version", "failure_detail").
			Returning("*").
			Scan(ctx)
		return classifyBuildError(err)
	})
	return created, err
}

// classifyBuildError maps the builds table's foreign key and CHECK
// constraints onto the repository's sentinel errors so callers see a 4xx
// instead of a bare 500 — a reviewId the caller's tenant cannot see fails the
// same foreign key check whether the row genuinely doesn't exist or just
// isn't this tenant's, matching the "doesn't exist or isn't visible" 404
// documented in collaboration/builds.md.
func classifyBuildError(err error) error {
	code, ok := pgErrorCode(err)
	if !ok {
		return err
	}
	switch code {
	case pgerrcode.ForeignKeyViolation:
		return ErrNotFound
	case pgerrcode.NotNullViolation, pgerrcode.CheckViolation:
		return ErrInvalidInput
	case pgerrcode.UniqueViolation:
		return ErrConflict
	default:
		return err
	}
}

func (r *BuildRepository) Get(ctx context.Context, buildID string) (model.Build, error) {
	var build model.Build
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT b.build_id, b.tenant_id, b.review_id, b.kind, b.successful, b.commit_id, b.version, b.failure_detail, b.created_at, b.updated_at, r.name AS review_name
			  FROM builds b
			  JOIN reviews r
			    ON r.tenant_id = b.tenant_id
			   AND r.review_id = b.review_id
			 WHERE b.build_id = ?
		`, buildID).Scan(ctx, &build)
		return normalizeNoRows(err)
	})
	return build, err
}

func (r *BuildRepository) List(ctx context.Context, filter BuildFilter) ([]model.Build, error) {
	var builds []model.Build
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		query := `
			SELECT b.build_id, b.tenant_id, b.review_id, b.kind, b.successful, b.commit_id, b.version, b.failure_detail, b.created_at, b.updated_at, r.name AS review_name
			  FROM builds b
			  JOIN reviews r
			    ON r.tenant_id = b.tenant_id
			   AND r.review_id = b.review_id
		`
		var args []any
		if filter.ReviewID != "" {
			query += ` WHERE b.review_id = ?`
			args = append(args, filter.ReviewID)
		}
		query += ` ORDER BY b.created_at DESC, b.build_id DESC`
		return tx.NewRaw(query, args...).Scan(ctx, &builds)
	})
	return builds, err
}
