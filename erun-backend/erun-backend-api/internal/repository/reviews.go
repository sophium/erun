package repository

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type ReviewRepository struct {
	txs *TxManager
}

const (
	reviewColumns          = `review_id, tenant_id, author_user_id, repository, name, target_branch, source_branch, status, last_failed_build_id, last_ready_build_id, last_merged_build_id, created_at, updated_at`
	qualifiedReviewColumns = `r.review_id, r.tenant_id, r.author_user_id, r.repository, r.name, r.target_branch, r.source_branch, r.status, r.last_failed_build_id, r.last_ready_build_id, r.last_merged_build_id, r.created_at, r.updated_at`
)

// ReviewFilter composes GET /v1/reviews discovery filters. Every field is
// optional and AND-ed together; ReviewerUserID is the only one that needs a
// join, since reviewers live in a separate table.
type ReviewFilter struct {
	// Repository narrows discovery to one repository's reviews. It is not
	// defaulted anywhere: a listing is how a caller finds work across every
	// repository their tenant serves.
	Repository     string
	TargetBranch   string
	SourceBranch   string
	Status         model.ReviewStatus
	AuthorUserID   string
	ReviewerUserID string
}

// scopedToRepository appends "this query is about repository X" to a condition
// list, or leaves it alone for an empty repository: a caller that named none
// is asking about every repository's queue, which is what a target branch
// alone has always meant. Reviews created before the platform recorded a
// repository hold NULL and so appear only in that unfiltered answer, rather
// than being guessed at as belonging to one.
func scopedToRepository(conditions []string, args []any, column, repository string) ([]string, []any) {
	if strings.TrimSpace(repository) == "" {
		return conditions, args
	}
	return append(conditions, column+" = ?"), append(args, repository)
}

func NewReviewRepository(txs *TxManager) *ReviewRepository {
	return &ReviewRepository{txs: txs}
}

func (r *ReviewRepository) Create(ctx context.Context, review model.Review) (model.Review, error) {
	created := review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewInsert().
			Model(&created).
			Column("repository", "name", "target_branch", "source_branch", "status").
			Returning("*").
			Scan(ctx)
		// Catches both the tenant/name uniqueness contract and the one-live-
		// review-per-source/target-branch partial unique index: a second live
		// proposal of the same change is a conflict with the review already
		// live, not a server error.
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	})
	return created, err
}

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
		conditions, args = scopedToRepository(conditions, args, "r.repository", filter.Repository)
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
// narrowed to one repository and one target branch. Scoped explicitly by
// tenant_id from the security context rather than left to RLS:
// erun_operations' policy is unconditional, so an OPERATIONS caller's empty
// targetBranch would otherwise read every tenant's merge queue.
//
// An empty repository narrows nothing, so the queue it answers spans every
// repository the tenant serves; a caller that names one gets that
// repository's queue, which is what separates two repositories that both
// propose into main.
func (r *ReviewRepository) ListMergeQueue(ctx context.Context, repository, targetBranch string) ([]model.Review, error) {
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
		if strings.TrimSpace(repository) != "" {
			query += ` AND r.repository = ?`
			args = append(args, repository)
		}
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
			Column("status", "last_failed_build_id", "last_ready_build_id", "last_merged_build_id", "repository").
			Where("review_id = ?", updated.ReviewID).
			Returning("*").
			Scan(ctx)
		return normalizeNoRows(err)
	})
	return updated, err
}

// FindNextMergeQueueReview returns the head of the queue waiting to merge
// into targetBranch in repository — the one a promotion would take. It is
// scoped by repository because two repositories a tenant serves may each have
// a queue for the same target branch, and promoting the wrong one's head is
// what gates a branch that does not exist in the checkout the gate runs in.
func (r *ReviewRepository) FindNextMergeQueueReview(ctx context.Context, repository, targetBranch string) (model.Review, error) {
	var review model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		conditions := []string{"q.target_branch = ?", "r.status = 'READY'"}
		args := []any{targetBranch}
		conditions, args = scopedToRepository(conditions, args, "r.repository", repository)
		err := tx.NewRaw(`
		SELECT `+qualifiedReviewColumns+`
		  FROM review_merge_queue q
		  JOIN reviews r
		    ON r.tenant_id = q.tenant_id
		   AND r.target_branch = q.target_branch
		   AND r.review_id = q.review_id
		 WHERE `+strings.Join(conditions, " AND ")+`
		 ORDER BY q.review_merge_queue_id ASC
		 LIMIT 1
	`, args...).Scan(ctx, &review)
		return normalizeNoRows(err)
	})
	return review, err
}

// FindActiveMergeReview returns the review currently holding MERGE on
// repository's targetBranch. Scoped by repository for the same reason the
// queue is: one repository's merge in flight says nothing about whether
// another's may start.
func (r *ReviewRepository) FindActiveMergeReview(ctx context.Context, repository, targetBranch string) (model.Review, error) {
	var review model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		conditions := []string{"target_branch = ?", "status = 'MERGE'"}
		args := []any{targetBranch}
		conditions, args = scopedToRepository(conditions, args, "repository", repository)
		err := tx.NewRaw(`
		SELECT `+reviewColumns+`
		  FROM reviews
		 WHERE `+strings.Join(conditions, " AND ")+`
		 LIMIT 1
	`, args...).Scan(ctx, &review)
		return normalizeNoRows(err)
	})
	return review, err
}

// FindLastMergedReview returns the most recently merged review for
// targetBranch in repository — the platform's own record of what the branch's
// tip was the last time a queue-driven merge landed on it. Scoped by
// repository so one repository's landing is not read as another's, which
// would refuse every merge whose history never passed through it. ErrNotFound
// means no review has ever merged onto this branch through the queue yet.
//
// A MERGED review with no last_merged_build_id is skipped: that is a review
// reconciled against the branch's history rather than merged through the
// queue, so it records no commit this could anchor on, and returning it would
// make gatedTargetTip resolve an empty build id — failing every subsequent
// queue-driven merge on the branch.
func (r *ReviewRepository) FindLastMergedReview(ctx context.Context, repository, targetBranch string) (model.Review, error) {
	var review model.Review
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		conditions := []string{"target_branch = ?", "status = 'MERGED'", "last_merged_build_id IS NOT NULL"}
		args := []any{targetBranch}
		conditions, args = scopedToRepository(conditions, args, "repository", repository)
		err := tx.NewRaw(`
		SELECT `+reviewColumns+`
		  FROM reviews
		 WHERE `+strings.Join(conditions, " AND ")+`
		 ORDER BY updated_at DESC, review_id DESC
		 LIMIT 1
	`, args...).Scan(ctx, &review)
		return normalizeNoRows(err)
	})
	return review, err
}

// QueuedRepositories returns the distinct repositories with a review waiting
// in targetBranch's queue. An unfiltered queue spanning more than one is not
// one queue, and promoting its head would gate a branch in a repository the
// caller never named; the service reads this before such a promotion.
//
// It is scoped to the caller's tenant explicitly, like ListMergeQueue and for
// the same reason: erun_operations' policy is unconditional.
func (r *ReviewRepository) QueuedRepositories(ctx context.Context, targetBranch string) ([]string, error) {
	var repositories []string
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		return tx.NewRaw(`
			SELECT DISTINCT COALESCE(r.repository, '')
			  FROM review_merge_queue q
			  JOIN reviews r
			    ON r.tenant_id = q.tenant_id
			   AND r.target_branch = q.target_branch
			   AND r.review_id = q.review_id
			 WHERE q.tenant_id = ?
			   AND q.target_branch = ?
			   AND r.status = 'READY'
		`, securityContext.TenantID, targetBranch).Scan(ctx, &repositories)
	})
	return repositories, err
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
