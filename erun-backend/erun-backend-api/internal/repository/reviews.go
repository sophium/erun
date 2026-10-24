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
	reviewColumns          = `review_id, tenant_id, author_user_id, repository, name, target_branch, source_branch, issue_ref, status, last_failed_build_id, last_ready_build_id, last_merged_build_id, created_at, updated_at`
	qualifiedReviewColumns = `r.review_id, r.tenant_id, r.author_user_id, r.repository, r.name, r.target_branch, r.source_branch, r.issue_ref, r.status, r.last_failed_build_id, r.last_ready_build_id, r.last_merged_build_id, r.created_at, r.updated_at`
)

// ReviewFilter composes GET /v1/reviews discovery filters, AND-ed together.
type ReviewFilter struct {
	Repository     string
	TargetBranch   string
	SourceBranch   string
	Status         model.ReviewStatus
	AuthorUserID   string
	ReviewerUserID string
}

// scopedToRepository filters by repository unless it is empty. Reviews with a
// NULL repository appear only in unfiltered results.
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
			Column("repository", "name", "target_branch", "source_branch", "issue_ref", "status").
			Returning("*").
			Scan(ctx)
		// Covers both the name index and the one-live-review-per-branch index.
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

// List returns the caller's tenant's reviews, scoped explicitly by tenant_id
// because erun_operations' RLS policy is unconditional.
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

// ListMergeQueue returns the caller's tenant's merge queue, scoped explicitly
// by tenant_id because erun_operations' RLS policy is unconditional. An empty
// repository spans every repository.
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

// FindNextMergeQueueReview returns the head of repository's queue into
// targetBranch; two repositories may queue into the same branch name.
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

// FindActiveMergeReview returns the review holding MERGE on repository's targetBranch.
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

// FindLastMergedReview returns the latest queue-merged review for targetBranch
// in repository. Reviews with no last_merged_build_id were reconciled, not
// merged, and are skipped since they anchor no commit.
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

// MergeQueueRepositories splits a waiting queue into named repositories and
// reviews with no recorded repository, which do not count as a repository.
type MergeQueueRepositories struct {
	Named []string
	// Unrecorded holds those reviews' ids in queue order.
	Unrecorded []string
}

// QueuedRepositories describes targetBranch's queue so the service can refuse
// an unfiltered promotion spanning several repositories. Tenant-scoped
// explicitly, like ListMergeQueue.
func (r *ReviewRepository) QueuedRepositories(ctx context.Context, targetBranch string) (MergeQueueRepositories, error) {
	var rows []struct {
		Repository string `bun:"repository"`
		ReviewID   string `bun:"review_id"`
	}
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		return tx.NewRaw(`
			SELECT COALESCE(r.repository, '') AS repository, r.review_id
			  FROM review_merge_queue q
			  JOIN reviews r
			    ON r.tenant_id = q.tenant_id
			   AND r.target_branch = q.target_branch
			   AND r.review_id = q.review_id
			 WHERE q.tenant_id = ?
			   AND q.target_branch = ?
			   AND r.status = 'READY'
			 ORDER BY q.review_merge_queue_id ASC
		`, securityContext.TenantID, targetBranch).Scan(ctx, &rows)
	})
	queued := MergeQueueRepositories{}
	seen := map[string]bool{}
	for _, row := range rows {
		repository := strings.TrimSpace(row.Repository)
		if repository == "" {
			queued.Unrecorded = append(queued.Unrecorded, row.ReviewID)
			continue
		}
		if !seen[repository] {
			seen[repository] = true
			queued.Named = append(queued.Named, repository)
		}
	}
	return queued, err
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
