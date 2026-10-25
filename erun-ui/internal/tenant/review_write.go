package tenant

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The write routes the dashboard gates on, in the same canonical
// "METHOD /path" form dashboard.go's read routes use.
const (
	tenantDashboardWriteCreateReview = "POST /v1/reviews"
	tenantDashboardWriteReviewStatus = "PATCH /v1/reviews/{review_id}/status"
	WriteAdvanceMergeQueue           = "POST /v1/reviews/merge-queue/advance"
	// Gated on its own route so a tenant can grant it more narrowly than advance.
	tenantDashboardWriteOverrideAdvanceMergeQueue = "POST /v1/reviews/merge-queue/override-advance"
	tenantDashboardWriteCommentStatus             = "PATCH /v1/reviews/{review_id}/comments/{comment_id}/status"
)

// CreateReview opens a review.
func (s *Service) CreateReview(input CreateReviewInput) (DashboardReview, error) {
	tenant, err := uimodel.RequireTenant("opening a review", input.Tenant)
	if err != nil {
		return DashboardReview{}, err
	}
	name := strings.TrimSpace(input.Name)
	targetBranch := strings.TrimSpace(input.TargetBranch)
	sourceBranch := strings.TrimSpace(input.SourceBranch)
	if name == "" || targetBranch == "" || sourceBranch == "" {
		return DashboardReview{}, fmt.Errorf("name, target branch, and source branch are required")
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return DashboardReview{}, err
	}
	defer cancel()

	review, err := client.CreateReview(requestCtx, model.PlatformCreateReviewParams{
		Repository: strings.TrimSpace(input.Repository),
		Name:       name, TargetBranch: targetBranch, SourceBranch: sourceBranch,
	})
	if err != nil {
		return DashboardReview{}, operatorPlatformError(actionCreateReview, err)
	}
	return tenantDashboardReview(review), nil
}

// CloseReview transitions a review to CLOSED without merging.
func (s *Service) CloseReview(input CloseReviewInput) (DashboardReview, error) {
	tenant, err := uimodel.RequireTenant("closing a review", input.Tenant)
	if err != nil {
		return DashboardReview{}, err
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return DashboardReview{}, fmt.Errorf("review id is required")
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return DashboardReview{}, err
	}
	defer cancel()

	review, err := client.UpdateReviewStatus(requestCtx, reviewID, platformclient.PlatformUpdateReviewStatusParams{Status: "CLOSED"})
	if err != nil {
		return DashboardReview{}, operatorPlatformError(actionCloseReview, err)
	}
	return tenantDashboardReview(review), nil
}

// mergeQueueAddress passes an empty repository through as every repository's
// queue; the platform refuses it only when genuinely ambiguous.
func mergeQueueAddress(repository, targetBranch string) (model.PlatformMergeQueueParams, error) {
	queue := model.PlatformMergeQueueParams{
		Repository:   strings.TrimSpace(repository),
		TargetBranch: strings.TrimSpace(targetBranch),
	}
	if queue.TargetBranch == "" {
		return model.PlatformMergeQueueParams{}, fmt.Errorf("target branch is required")
	}
	return queue, nil
}

// AdvanceMergeQueue advances the queue head to MERGE. Unresolved threads come
// back as a Blocked result, and an occupied MERGE slot surfaces the platform's
// message, since retrying cannot help.
func (s *Service) AdvanceMergeQueue(input AdvanceMergeQueueInput) (DashboardReview, error) {
	tenant, err := uimodel.RequireTenant("advancing the merge queue", input.Tenant)
	if err != nil {
		return DashboardReview{}, err
	}
	queue, err := mergeQueueAddress(input.Repository, input.TargetBranch)
	if err != nil {
		return DashboardReview{}, err
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return DashboardReview{}, err
	}
	defer cancel()

	review, err := client.AdvanceMergeQueue(requestCtx, queue)
	if err != nil {
		var blocked *platformclient.PlatformMergeQueueBlockedError
		if errors.As(err, &blocked) {
			unresolved := blocked.UnresolvedThreads
			return DashboardReview{ReviewID: blocked.ReviewID, Blocked: true, UnresolvedThreads: &unresolved}, nil
		}
		var occupied *platformclient.PlatformMergeQueueOccupiedError
		if errors.As(err, &occupied) {
			return DashboardReview{}, errors.New(occupied.Error())
		}
		return DashboardReview{}, operatorPlatformError(actionAdvanceQueue, err)
	}
	return tenantDashboardReview(review), nil
}

// OverrideAdvanceMergeQueue bypasses the unresolved-thread gate; Reason is
// required and audited.
func (s *Service) OverrideAdvanceMergeQueue(input OverrideAdvanceMergeQueueInput) (DashboardReview, error) {
	tenant, err := uimodel.RequireTenant("overriding the merge queue's unresolved-thread gate", input.Tenant)
	if err != nil {
		return DashboardReview{}, err
	}
	queue, err := mergeQueueAddress(input.Repository, input.TargetBranch)
	if err != nil {
		return DashboardReview{}, err
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return DashboardReview{}, fmt.Errorf("a reason is required to override the merge queue's unresolved-thread gate")
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return DashboardReview{}, err
	}
	defer cancel()

	review, err := client.OverrideAdvanceMergeQueue(requestCtx, queue, reason)
	if err != nil {
		return DashboardReview{}, operatorPlatformError(actionOverrideAdvanceQueue, err)
	}
	return tenantDashboardReview(review), nil
}

// AddReviewer assigns userId as a reviewer; the picker offers only the tenant's
// enrolled users.
func (s *Service) AddReviewer(input AddReviewerInput) (Reviewer, error) {
	tenant, err := uimodel.RequireTenant("assigning a reviewer", input.Tenant)
	if err != nil {
		return Reviewer{}, err
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return Reviewer{}, fmt.Errorf("review id is required")
	}
	userID := strings.TrimSpace(input.UserID)
	if userID == "" {
		return Reviewer{}, fmt.Errorf("user id is required")
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return Reviewer{}, err
	}
	defer cancel()

	reviewer, err := client.AddReviewer(requestCtx, reviewID, platformclient.PlatformAddReviewerParams{UserID: userID})
	if err != nil {
		return Reviewer{}, operatorPlatformError(actionAddReviewer, err)
	}
	return Reviewer{UserID: reviewer.UserID}, nil
}

// RemoveReviewer unassigns userId from a review's reviewers.
func (s *Service) RemoveReviewer(input RemoveReviewerInput) error {
	tenant, err := uimodel.RequireTenant("removing a reviewer", input.Tenant)
	if err != nil {
		return err
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return fmt.Errorf("review id is required")
	}
	userID := strings.TrimSpace(input.UserID)
	if userID == "" {
		return fmt.Errorf("user id is required")
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return err
	}
	defer cancel()

	if err := client.RemoveReviewer(requestCtx, reviewID, userID); err != nil {
		return operatorPlatformError(actionRemoveReviewer, err)
	}
	return nil
}

// validateCreateReviewCommentInput is split out to stay under the complexity cap.
func validateCreateReviewCommentInput(tenant, reviewID, commitID, filePath, body string) error {
	if _, err := uimodel.RequireTenant("commenting on a review", tenant); err != nil {
		return err
	}
	if reviewID == "" {
		return fmt.Errorf("review id is required")
	}
	if commitID == "" || filePath == "" {
		return fmt.Errorf("a diff line anchor (commit and file path) is required")
	}
	if body == "" {
		return fmt.Errorf("comment body is required")
	}
	return nil
}

// CreateReviewComment starts a new top-level thread anchored to a diff line.
func (s *Service) CreateReviewComment(input CreateReviewCommentInput) (ReviewComment, error) {
	tenant := strings.TrimSpace(input.Tenant)
	reviewID := strings.TrimSpace(input.ReviewID)
	commitID := strings.TrimSpace(input.CommitID)
	filePath := strings.TrimSpace(input.FilePath)
	body := strings.TrimSpace(input.Body)
	if err := validateCreateReviewCommentInput(tenant, reviewID, commitID, filePath, body); err != nil {
		return ReviewComment{}, err
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return ReviewComment{}, err
	}
	defer cancel()

	comment, err := client.CreateComment(requestCtx, reviewID, platformclient.PlatformCreateCommentParams{
		CommitID: commitID,
		FilePath: filePath,
		Line:     input.Line,
		Body:     body,
	})
	if err != nil {
		return ReviewComment{}, operatorPlatformError(actionCommentReview, err)
	}
	return tenantDashboardComment(comment), nil
}

// ResolveReviewComment marks a thread CLOSED; the UI offers it only on root
// comments.
func (s *Service) ResolveReviewComment(input UpdateReviewCommentStatusInput) (ReviewComment, error) {
	return s.updateReviewCommentStatus(input, "CLOSED")
}

// UnresolveReviewComment reopens a comment thread (marks it OPEN).
func (s *Service) UnresolveReviewComment(input UpdateReviewCommentStatusInput) (ReviewComment, error) {
	return s.updateReviewCommentStatus(input, "OPEN")
}

func (s *Service) updateReviewCommentStatus(input UpdateReviewCommentStatusInput, status string) (ReviewComment, error) {
	operation := "resolving a review comment"
	if status == "OPEN" {
		operation = "reopening a review comment"
	}
	tenant, err := uimodel.RequireTenant(operation, input.Tenant)
	if err != nil {
		return ReviewComment{}, err
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return ReviewComment{}, fmt.Errorf("review id is required")
	}
	commentID := strings.TrimSpace(input.CommentID)
	if commentID == "" {
		return ReviewComment{}, fmt.Errorf("comment id is required")
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return ReviewComment{}, err
	}
	defer cancel()

	action := actionResolveComment
	if status == "OPEN" {
		action = actionUnresolveComment
	}
	comment, err := client.UpdateCommentStatus(requestCtx, reviewID, commentID, platformclient.PlatformUpdateCommentStatusParams{Status: status})
	if err != nil {
		return ReviewComment{}, operatorPlatformError(action, err)
	}
	return tenantDashboardComment(comment), nil
}
