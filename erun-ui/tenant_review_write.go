package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// The write routes the dashboard gates on, in the same canonical
// "METHOD /path" form tenant_dashboard.go's read routes use.
const (
	tenantDashboardWriteCreateReview      = "POST /v1/reviews"
	tenantDashboardWriteReviewStatus      = "PATCH /v1/reviews/{review_id}/status"
	tenantDashboardWriteAdvanceMergeQueue = "POST /v1/reviews/merge-queue/advance"
	// Gated on its own route so a tenant can grant it more narrowly than advance.
	tenantDashboardWriteOverrideAdvanceMergeQueue = "POST /v1/reviews/merge-queue/override-advance"
	tenantDashboardWriteCommentStatus             = "PATCH /v1/reviews/{review_id}/comments/{comment_id}/status"
)

// CreateReview opens a review.
func (a *App) CreateReview(input uiCreateReviewInput) (uiTenantDashboardReview, error) {
	tenant, err := requireTenant("opening a review", input.Tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	name := strings.TrimSpace(input.Name)
	targetBranch := strings.TrimSpace(input.TargetBranch)
	sourceBranch := strings.TrimSpace(input.SourceBranch)
	if name == "" || targetBranch == "" || sourceBranch == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("name, target branch, and source branch are required")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	defer cancel()

	review, err := client.CreateReview(requestCtx, model.PlatformCreateReviewParams{
		Repository: strings.TrimSpace(input.Repository),
		Name:       name, TargetBranch: targetBranch, SourceBranch: sourceBranch,
	})
	if err != nil {
		return uiTenantDashboardReview{}, operatorPlatformError(actionCreateReview, err)
	}
	return tenantDashboardReview(review), nil
}

// CloseReview transitions a review to CLOSED without merging.
func (a *App) CloseReview(input uiCloseReviewInput) (uiTenantDashboardReview, error) {
	tenant, err := requireTenant("closing a review", input.Tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("review id is required")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	defer cancel()

	review, err := client.UpdateReviewStatus(requestCtx, reviewID, platformclient.PlatformUpdateReviewStatusParams{Status: "CLOSED"})
	if err != nil {
		return uiTenantDashboardReview{}, operatorPlatformError(actionCloseReview, err)
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
func (a *App) AdvanceMergeQueue(input uiAdvanceMergeQueueInput) (uiTenantDashboardReview, error) {
	tenant, err := requireTenant("advancing the merge queue", input.Tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	queue, err := mergeQueueAddress(input.Repository, input.TargetBranch)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	defer cancel()

	review, err := client.AdvanceMergeQueue(requestCtx, queue)
	if err != nil {
		var blocked *platformclient.PlatformMergeQueueBlockedError
		if errors.As(err, &blocked) {
			unresolved := blocked.UnresolvedThreads
			return uiTenantDashboardReview{ReviewID: blocked.ReviewID, Blocked: true, UnresolvedThreads: &unresolved}, nil
		}
		var occupied *platformclient.PlatformMergeQueueOccupiedError
		if errors.As(err, &occupied) {
			return uiTenantDashboardReview{}, errors.New(occupied.Error())
		}
		return uiTenantDashboardReview{}, operatorPlatformError(actionAdvanceQueue, err)
	}
	return tenantDashboardReview(review), nil
}

// OverrideAdvanceMergeQueue bypasses the unresolved-thread gate; Reason is
// required and audited.
func (a *App) OverrideAdvanceMergeQueue(input uiOverrideAdvanceMergeQueueInput) (uiTenantDashboardReview, error) {
	tenant, err := requireTenant("overriding the merge queue's unresolved-thread gate", input.Tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	queue, err := mergeQueueAddress(input.Repository, input.TargetBranch)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("a reason is required to override the merge queue's unresolved-thread gate")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	defer cancel()

	review, err := client.OverrideAdvanceMergeQueue(requestCtx, queue, reason)
	if err != nil {
		return uiTenantDashboardReview{}, operatorPlatformError(actionOverrideAdvanceQueue, err)
	}
	return tenantDashboardReview(review), nil
}

// AddReviewer assigns userId as a reviewer; the picker offers only the tenant's
// enrolled users.
func (a *App) AddReviewer(input uiAddReviewerInput) (uiReviewer, error) {
	tenant, err := requireTenant("assigning a reviewer", input.Tenant)
	if err != nil {
		return uiReviewer{}, err
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return uiReviewer{}, fmt.Errorf("review id is required")
	}
	userID := strings.TrimSpace(input.UserID)
	if userID == "" {
		return uiReviewer{}, fmt.Errorf("user id is required")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiReviewer{}, err
	}
	defer cancel()

	reviewer, err := client.AddReviewer(requestCtx, reviewID, platformclient.PlatformAddReviewerParams{UserID: userID})
	if err != nil {
		return uiReviewer{}, operatorPlatformError(actionAddReviewer, err)
	}
	return uiReviewer{UserID: reviewer.UserID}, nil
}

// RemoveReviewer unassigns userId from a review's reviewers.
func (a *App) RemoveReviewer(input uiRemoveReviewerInput) error {
	tenant, err := requireTenant("removing a reviewer", input.Tenant)
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

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
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
	if _, err := requireTenant("commenting on a review", tenant); err != nil {
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
func (a *App) CreateReviewComment(input uiCreateReviewCommentInput) (uiReviewComment, error) {
	tenant := strings.TrimSpace(input.Tenant)
	reviewID := strings.TrimSpace(input.ReviewID)
	commitID := strings.TrimSpace(input.CommitID)
	filePath := strings.TrimSpace(input.FilePath)
	body := strings.TrimSpace(input.Body)
	if err := validateCreateReviewCommentInput(tenant, reviewID, commitID, filePath, body); err != nil {
		return uiReviewComment{}, err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiReviewComment{}, err
	}
	defer cancel()

	comment, err := client.CreateComment(requestCtx, reviewID, platformclient.PlatformCreateCommentParams{
		CommitID: commitID,
		FilePath: filePath,
		Line:     input.Line,
		Body:     body,
	})
	if err != nil {
		return uiReviewComment{}, operatorPlatformError(actionCommentReview, err)
	}
	return tenantDashboardComment(comment), nil
}

// ResolveReviewComment marks a thread CLOSED; the UI offers it only on root
// comments.
func (a *App) ResolveReviewComment(input uiUpdateReviewCommentStatusInput) (uiReviewComment, error) {
	return a.updateReviewCommentStatus(input, "CLOSED")
}

// UnresolveReviewComment reopens a comment thread (marks it OPEN).
func (a *App) UnresolveReviewComment(input uiUpdateReviewCommentStatusInput) (uiReviewComment, error) {
	return a.updateReviewCommentStatus(input, "OPEN")
}

func (a *App) updateReviewCommentStatus(input uiUpdateReviewCommentStatusInput, status string) (uiReviewComment, error) {
	operation := "resolving a review comment"
	if status == "OPEN" {
		operation = "reopening a review comment"
	}
	tenant, err := requireTenant(operation, input.Tenant)
	if err != nil {
		return uiReviewComment{}, err
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return uiReviewComment{}, fmt.Errorf("review id is required")
	}
	commentID := strings.TrimSpace(input.CommentID)
	if commentID == "" {
		return uiReviewComment{}, fmt.Errorf("comment id is required")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiReviewComment{}, err
	}
	defer cancel()

	action := actionResolveComment
	if status == "OPEN" {
		action = actionUnresolveComment
	}
	comment, err := client.UpdateCommentStatus(requestCtx, reviewID, commentID, platformclient.PlatformUpdateCommentStatusParams{Status: status})
	if err != nil {
		return uiReviewComment{}, operatorPlatformError(action, err)
	}
	return tenantDashboardComment(comment), nil
}
