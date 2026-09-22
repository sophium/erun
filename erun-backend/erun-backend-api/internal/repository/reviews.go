package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type ReviewRepository struct {
	txs *TxManager
}

const (
	reviewColumns          = `review_id, tenant_id, author_user_id, name, target_branch, source_branch, status, last_failed_build_id, last_ready_build_id, last_merged_build_id, created_at, updated_at`
	qualifiedReviewColumns = `r.review_id, r.tenant_id, r.author_user_id, r.name, r.target_branch, r.source_branch, r.status, r.last_failed_build_id, r.last_ready_build_id, r.last_merged_build_id, r.created_at, r.updated_at`
)

// ReviewFilter composes GET /v1/reviews discovery filters. Every field is
// optional and AND-ed together; ReviewerUserID is the only one that needs a
// join, since reviewers live in a separate table.
type ReviewFilter struct {
	TargetBranch   string
	SourceBranch   string
	Status         model.ReviewStatus
	AuthorUserID   string
	ReviewerUserID string
}

func NewReviewRepository(txs *TxManager) *ReviewRepository {
	return &ReviewRepository{txs: txs}
}

func (r *ReviewRepository) Create(ctx context.Context, review model.Review) (model.Review, error) {
	created := review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewInsert().
			Model(&created).
			Column("name", "target_branch", "source_branch", "status").
			Returning("*").
			Scan(ctx)
		// Catches both the tenant/name uniqueness contract and the one-live-
		// review-per-source/target-branch partial unique index: a second live
		// proposal of the same change is a conflict with the review already
		// live, not a server error. The two are told apart so a caller is told
		// which one fired — a name collision names the name, a branch-pair
		// collision names neither remedy the other needs.
		if isUniqueViolation(err) {
			return reviewConflictError(err, created)
		}
		return err
	})
	return created, err
}

// reviewConflictError names which of reviews' two uniqueness contracts a write
// violated. An unrecognized constraint is reported as genuinely unknown rather
// than guessed at as the most likely-sounding cause, the same discipline
// UserRepository.Create applies to users' two.
func reviewConflictError(err error, review model.Review) error {
	constraint, ok := pgConstraintName(err)
	if !ok {
		return ErrConflict
	}
	switch constraint {
	case "reviews_tenant_name_idx":
		return &ReviewNameConflictError{Name: review.Name}
	case "reviews_tenant_live_source_target_idx":
		return &ReviewBranchPairConflictError{SourceBranch: review.SourceBranch, TargetBranch: review.TargetBranch}
	default:
		return ErrConflict
	}
}

// ReviewNameConflictError refuses a review whose name a review that can still
// land already holds. It carries the name so the refusal can say which one,
// which is what the bare "Conflict" body this replaces never did.
type ReviewNameConflictError struct {
	Name string
}

func (e *ReviewNameConflictError) Error() string {
	return fmt.Sprintf("a review named %q already exists in this tenant and can still land; close it, or choose another name", e.Name)
}

func (e *ReviewNameConflictError) Unwrap() error { return ErrConflict }

// ReviewBranchPairConflictError refuses a second live review proposing a
// branch pair one already proposes.
type ReviewBranchPairConflictError struct {
	SourceBranch string
	TargetBranch string
}

func (e *ReviewBranchPairConflictError) Error() string {
	return fmt.Sprintf("a live review already proposes %s onto %s; complete or close it before opening another", e.SourceBranch, e.TargetBranch)
}

func (e *ReviewBranchPairConflictError) Unwrap() error { return ErrConflict }

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

// List returns the caller's tenant's reviews matching filter. Scoped
// explicitly by tenant_id from the security context rather than left to RLS:
// erun_operations' policy is unconditional, so an OPERATIONS caller's empty
// filter would otherwise read every tenant's reviews.
func (r *ReviewRepository) List(ctx context.Context, filter ReviewFilter) ([]model.Review, error) {
	var reviews []model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query := `SELECT ` + qualifiedReviewColumns + ` FROM reviews r`
		conditions := []string{"r.tenant_id = ?"}
		args := []any{securityContext.TenantID}
		if filter.ReviewerUserID != "" {
			query += `
				  JOIN review_reviewers rr
				    ON rr.tenant_id = r.tenant_id
				   AND rr.review_id = r.review_id
			`
			conditions = append(conditions, "rr.user_id = ?")
			args = append(args, filter.ReviewerUserID)
		}
		if filter.TargetBranch != "" {
			conditions = append(conditions, "r.target_branch = ?")
			args = append(args, filter.TargetBranch)
		}
		if filter.SourceBranch != "" {
			conditions = append(conditions, "r.source_branch = ?")
			args = append(args, filter.SourceBranch)
		}
		if filter.Status != "" {
			conditions = append(conditions, "r.status = ?")
			args = append(args, filter.Status)
		}
		if filter.AuthorUserID != "" {
			conditions = append(conditions, "r.author_user_id = ?")
			args = append(args, filter.AuthorUserID)
		}
		query += " WHERE " + strings.Join(conditions, " AND ")
		query += ` ORDER BY r.created_at DESC, r.review_id DESC`
		return tx.NewRaw(query, args...).Scan(ctx, &reviews)
	})
	return reviews, err
}

// ListMergeQueue returns the caller's tenant's merge queue, optionally
// narrowed to one target branch. Scoped explicitly by tenant_id from the
// security context rather than left to RLS: erun_operations' policy is
// unconditional, so an OPERATIONS caller's empty targetBranch would otherwise
// read every tenant's merge queue.
func (r *ReviewRepository) ListMergeQueue(ctx context.Context, targetBranch string) ([]model.Review, error) {
	var reviews []model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		query := `
			SELECT ` + qualifiedReviewColumns + `
			  FROM review_merge_queue q
			  JOIN reviews r
			    ON r.tenant_id = q.tenant_id
			   AND r.target_branch = q.target_branch
			   AND r.review_id = q.review_id
			 WHERE q.tenant_id = ?
			   AND r.status = 'READY'
		`
		args := []any{securityContext.TenantID}
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
		SELECT `+reviewColumns+`
		  FROM reviews
		 WHERE target_branch = ?
		   AND status = 'MERGE'
		 LIMIT 1
	`, targetBranch).Scan(ctx, &review)
		return normalizeNoRows(err)
	})
	return review, err
}

// FindLastMergedReview returns the most recently merged review for
// targetBranch — the platform's own record of what the branch's tip was the
// last time a queue-driven merge landed on it. ErrNotFound means no review
// has ever merged onto this branch through the queue yet.
//
// A MERGED review with no last_merged_build_id is skipped: that is a review
// reconciled against the branch's history rather than merged through the
// queue, so it records no commit this could anchor on, and returning it would
// make gatedTargetTip resolve an empty build id — failing every subsequent
// queue-driven merge on the branch.
func (r *ReviewRepository) FindLastMergedReview(ctx context.Context, targetBranch string) (model.Review, error) {
	var review model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
		SELECT `+reviewColumns+`
		  FROM reviews
		 WHERE target_branch = ?
		   AND status = 'MERGED'
		   AND last_merged_build_id IS NOT NULL
		 ORDER BY updated_at DESC, review_id DESC
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
