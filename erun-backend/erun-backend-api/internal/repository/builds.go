package repository

import (
	"context"
	"database/sql"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type BuildRepository struct {
	txs *TxManager
}

func NewBuildRepository(txs *TxManager) *BuildRepository {
	return &BuildRepository{txs: txs}
}

func (r *BuildRepository) Create(ctx context.Context, build model.Build) (model.Build, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Build{}, ErrMissingSecurityContext
	}
	build.TenantID = securityContext.TenantID
	if build.BuildID == "" {
		buildID, err := newUUIDv7()
		if err != nil {
			return model.Build{}, err
		}
		build.BuildID = buildID
	}

	var created model.Build
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		created, err = scanBuild(tx.QueryRowContext(ctx, r.txs.rebind(`
			INSERT INTO builds (build_id, tenant_id, review_id, successful, commit_id, version)
			VALUES (?, ?, ?, ?, ?, ?)
			RETURNING build_id, tenant_id, review_id, successful, commit_id, version, created_at, updated_at
		`), build.BuildID, build.TenantID, build.ReviewID, build.Successful, build.CommitID, build.Version))
		if err != nil {
			return err
		}

		return NewReviewRepository(r.txs).markBuildResult(ctx, tx, build.TenantID, build.ReviewID, build.BuildID, build.Successful)
	})
	return created, err
}

func (r *BuildRepository) Get(ctx context.Context, reviewID string, buildID string) (model.Build, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Build{}, ErrMissingSecurityContext
	}

	var build model.Build
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		build, err = scanBuild(tx.QueryRowContext(ctx, r.txs.rebind(`
			SELECT build_id, tenant_id, review_id, successful, commit_id, version, created_at, updated_at
			  FROM builds
			 WHERE tenant_id = ?
			   AND review_id = ?
			   AND build_id = ?
		`), securityContext.TenantID, reviewID, buildID))
		return err
	})
	return build, err
}

func (r *BuildRepository) ListByReview(ctx context.Context, reviewID string) ([]model.Build, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return nil, ErrMissingSecurityContext
	}

	var builds []model.Build
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, r.txs.rebind(`
			SELECT build_id, tenant_id, review_id, successful, commit_id, version, created_at, updated_at
			  FROM builds
			 WHERE tenant_id = ?
			   AND review_id = ?
			 ORDER BY created_at DESC, build_id DESC
		`), securityContext.TenantID, reviewID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			build, err := scanBuild(rows)
			if err != nil {
				return err
			}
			builds = append(builds, build)
		}
		return rows.Err()
	})
	return builds, err
}

func scanBuild(row rowScanner) (model.Build, error) {
	var build model.Build
	err := row.Scan(
		&build.BuildID,
		&build.TenantID,
		&build.ReviewID,
		&build.Successful,
		&build.CommitID,
		&build.Version,
		scanTime(&build.CreatedAt),
		scanTime(&build.UpdatedAt),
	)
	if err != nil {
		return model.Build{}, normalizeNoRows(err)
	}
	return build, nil
}
