package repository

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

const (
	defaultBuildLimit = 50
	maxBuildLimit     = 200
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
			Column("review_id", "environment_id", "kind", "successful", "commit_id", "version", "failure_detail", "profile").
			Returning("*").
			Scan(ctx)
		return classifyBuildError(err)
	})
	return created, err
}

// classifyBuildError maps constraint violations to sentinel errors; an invisible
// reviewId fails the FK like a missing one, yielding 404.
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

// Get returns a build owned by tenantID. tenantID is an explicit predicate
// because an OPERATIONS caller's RLS policy does not scope the read.
func (r *BuildRepository) Get(ctx context.Context, tenantID, buildID string) (model.Build, error) {
	var build model.Build
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT b.build_id, b.tenant_id, b.review_id, b.kind, b.successful, b.commit_id, b.version, b.failure_detail, b.profile, b.created_at, b.updated_at, r.name AS review_name
			  FROM builds b
			  JOIN reviews r
			    ON r.tenant_id = b.tenant_id
			   AND r.review_id = b.review_id
			 WHERE b.build_id = ?
			   AND b.tenant_id = ?
		`, buildID, tenantID).Scan(ctx, &build)
		return normalizeNoRows(err)
	})
	return build, err
}

// List returns the tenant's builds, optionally for one review. tenant_id is
// filtered explicitly because erun_operations' RLS policy is unconditional.
func (r *BuildRepository) List(ctx context.Context, filter BuildFilter) ([]model.Build, error) {
	var builds []model.Build
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query := `
			SELECT b.build_id, b.tenant_id, b.review_id, b.kind, b.successful, b.commit_id, b.version, b.failure_detail, b.profile, b.created_at, b.updated_at, r.name AS review_name
			  FROM builds b
			  JOIN reviews r
			    ON r.tenant_id = b.tenant_id
			   AND r.review_id = b.review_id
			 WHERE b.tenant_id = ?
		`
		args := []any{securityContext.TenantID}
		if filter.ReviewID != "" {
			query += ` AND b.review_id = ?`
			args = append(args, filter.ReviewID)
		}
		query += ` ORDER BY b.created_at DESC, b.build_id DESC`
		return tx.NewRaw(query, args...).Scan(ctx, &builds)
	})
	return builds, err
}

// buildListColumns LEFT JOINs review and environment names because both ids are nullable.
const buildListColumns = `b.build_id, b.tenant_id, b.review_id, b.environment_id, b.kind, b.successful, b.commit_id, b.version, b.failure_detail, b.profile, b.created_at, b.updated_at, r.name AS review_name, e.name AS environment_name`

// BuildCursor is a keyset position in ListPage's (created_at DESC, build_id
// DESC) order; the zero value starts at the newest row.
type BuildCursor struct {
	CreatedAt time.Time
	BuildID   string
}

func (c BuildCursor) isZero() bool {
	return c.CreatedAt.IsZero() && c.BuildID == ""
}

// String encodes the cursor as an opaque token.
func (c BuildCursor) String() string {
	if c.isZero() {
		return ""
	}
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "," + c.BuildID
}

// ParseBuildCursor decodes a cursor token; empty decodes to the zero cursor.
func ParseBuildCursor(token string) (BuildCursor, error) {
	if token == "" {
		return BuildCursor{}, nil
	}
	createdAt, buildID, ok := strings.Cut(token, ",")
	if !ok || buildID == "" {
		return BuildCursor{}, ErrInvalidInput
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return BuildCursor{}, ErrInvalidInput
	}
	return BuildCursor{CreatedAt: parsed, BuildID: buildID}, nil
}

// BuildListFilter narrows a page of the tenant-wide build history (GET /v1/builds).
type BuildListFilter struct {
	EnvironmentID string
	Kind          model.BuildKind
	CommitID      string
	// Successful is nil for "either".
	Successful *bool
	Since      time.Time
	Until      time.Time
	Cursor     BuildCursor
	Limit      int
}

// BuildPage is one newest-first page; NextCursor is empty at the end.
type BuildPage struct {
	Builds     []model.Build
	NextCursor string
}

// ListPage returns one page of the tenant's build history, newest first.
// tenant_id is filtered explicitly because erun_operations' RLS policy is unconditional.
func (r *BuildRepository) ListPage(ctx context.Context, filter BuildListFilter) (BuildPage, error) {
	limit := filter.Limit
	switch {
	case limit <= 0:
		limit = defaultBuildLimit
	case limit > maxBuildLimit:
		limit = maxBuildLimit
	}

	var builds []model.Build
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query, args := buildListPageQuery(securityContext.TenantID, filter, limit)
		return tx.NewRaw(query, args...).Scan(ctx, &builds)
	})
	if err != nil {
		return BuildPage{}, err
	}

	page := BuildPage{Builds: builds}
	if len(builds) > limit {
		page.Builds = builds[:limit]
		last := page.Builds[len(page.Builds)-1]
		page.NextCursor = BuildCursor{CreatedAt: last.CreatedAt, BuildID: last.BuildID}.String()
	}
	return page, nil
}

// buildListPageQuery fetches limit+1 rows so ListPage can detect a next page.
func buildListPageQuery(tenantID string, filter BuildListFilter, limit int) (string, []any) {
	query := `
		SELECT ` + buildListColumns + `
		  FROM builds b
		  LEFT JOIN reviews r ON r.tenant_id = b.tenant_id AND r.review_id = b.review_id
		  LEFT JOIN environments e ON e.tenant_id = b.tenant_id AND e.environment_id = b.environment_id
		 WHERE b.tenant_id = ?
	`
	args := []any{tenantID}
	if filter.EnvironmentID != "" {
		query += ` AND b.environment_id = ?`
		args = append(args, filter.EnvironmentID)
	}
	if filter.Kind != "" {
		query += ` AND b.kind = ?`
		args = append(args, string(filter.Kind))
	}
	if filter.CommitID != "" {
		query += ` AND b.commit_id = ?`
		args = append(args, filter.CommitID)
	}
	if filter.Successful != nil {
		query += ` AND b.successful = ?`
		args = append(args, *filter.Successful)
	}
	if !filter.Since.IsZero() {
		query += ` AND b.created_at >= ?`
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		query += ` AND b.created_at <= ?`
		args = append(args, filter.Until)
	}
	if !filter.Cursor.isZero() {
		query += ` AND (b.created_at, b.build_id) < (?, ?)`
		args = append(args, filter.Cursor.CreatedAt, filter.Cursor.BuildID)
	}
	query += ` ORDER BY b.created_at DESC, b.build_id DESC LIMIT ?`
	args = append(args, limit+1)
	return query, args
}
