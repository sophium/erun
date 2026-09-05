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
			Column("review_id", "environment_id", "kind", "successful", "commit_id", "version", "failure_detail").
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

// List returns the caller's tenant's builds, optionally narrowed to one
// review. Scoped explicitly by tenant_id from the security context rather
// than left to RLS: erun_operations' policy is unconditional, so an
// OPERATIONS caller's empty filter would otherwise read every tenant's
// builds.
func (r *BuildRepository) List(ctx context.Context, filter BuildFilter) ([]model.Build, error) {
	var builds []model.Build
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query := `
			SELECT b.build_id, b.tenant_id, b.review_id, b.kind, b.successful, b.commit_id, b.version, b.failure_detail, b.created_at, b.updated_at, r.name AS review_name
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

// buildListColumns is the SELECT list ListPage and List share, with the
// review/environment display names both LEFT JOINed rather than INNER
// JOINed: an ordinary erun build's review_id and environment_id are each
// independently nullable, so an INNER JOIN on either would silently drop
// exactly the rows this feature exists to surface.
const buildListColumns = `b.build_id, b.tenant_id, b.review_id, b.environment_id, b.kind, b.successful, b.commit_id, b.version, b.failure_detail, b.created_at, b.updated_at, r.name AS review_name, e.name AS environment_name`

// BuildCursor identifies a row's position in the newest-first (created_at
// DESC, build_id DESC) ordering ListPage always uses. The zero value means
// "start from the newest row" -- builds is unbounded and grows continuously
// (every `erun build` run across every environment reports one), so offset
// pagination would degrade as it grows and would skip or repeat rows as new
// builds are appended ahead of a later page, the same reasoning
// AuditEventCursor documents.
type BuildCursor struct {
	CreatedAt time.Time
	BuildID   string
}

func (c BuildCursor) isZero() bool {
	return c.CreatedAt.IsZero() && c.BuildID == ""
}

// String encodes the cursor as an opaque token for a caller to hand back to
// resume after the last row it saw.
func (c BuildCursor) String() string {
	if c.isZero() {
		return ""
	}
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "," + c.BuildID
}

// ParseBuildCursor decodes a token previously returned by ListPage. An empty
// token decodes to the zero cursor.
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

// BuildListFilter narrows a page of the caller's tenant-wide build history
// (GET /v1/builds) -- unlike BuildFilter/List above, which the review-nested
// route uses and which stays a small, naturally-bounded, unpaginated read.
type BuildListFilter struct {
	EnvironmentID string
	Kind          model.BuildKind
	// Successful is nil for "either", so a caller can distinguish "only
	// failed builds" from "no opinion" the same way a bare bool could not.
	Successful *bool
	Since      time.Time
	Until      time.Time
	Cursor     BuildCursor
	// Limit caps the page size; non-positive defaults to defaultBuildLimit and
	// any value above maxBuildLimit is capped there.
	Limit int
}

// BuildPage is one newest-first page of a tenant's build history. NextCursor
// is empty when the page reached the end of the history.
type BuildPage struct {
	Builds     []model.Build
	NextCursor string
}

// ListPage returns one page of the caller's tenant's whole build history,
// review-linked and unattached alike, newest first. Scoped explicitly by
// tenant_id from the security context rather than left to RLS: erun_operations'
// policy is unconditional, so an OPERATIONS caller's empty filter would
// otherwise read every tenant's builds.
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

// buildListPageQuery builds the SELECT for ListPage. It fetches one row
// beyond limit so a next page can be reported without a second round trip;
// ListPage trims that extra row back off.
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
