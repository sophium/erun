package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// tenant_review_write.go gives the desktop the review write surface the CLI's
// `erun review create|close|merge-queue advance` and the equivalent MCP tools
// already have: opening a review, closing one, and advancing a target
// branch's merge queue, all through the same eruncommon.PlatformClient the
// dashboard's reads already use.

// The write routes the dashboard gates on, in the same canonical
// "METHOD /path" form tenant_dashboard.go's read routes use.
const (
	tenantDashboardWriteCreateReview      = "POST /v1/reviews"
	tenantDashboardWriteReviewStatus      = "PATCH /v1/reviews/{review_id}/status"
	tenantDashboardWriteAdvanceMergeQueue = "POST /v1/reviews/merge-queue/advance"
	// The platform gates this on its own, distinct route so a tenant can grant
	// it to a narrower set of roles than ordinary advance (see erun-backend-api
	// AGENTS.md "Merge Queue").
	tenantDashboardWriteOverrideAdvanceMergeQueue = "POST /v1/reviews/merge-queue/override-advance"
	tenantDashboardWriteCommentStatus             = "PATCH /v1/reviews/{review_id}/comments/{comment_id}/status"
)

// CreateReview opens a review. It is a real, immediate write — there is no
// dashboard preview path, matching every other side-effecting action here.
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

	review, err := client.CreateReview(requestCtx, eruncommon.PlatformCreateReviewParams{
		Name: name, TargetBranch: targetBranch, SourceBranch: sourceBranch,
	})
	if err != nil {
		return uiTenantDashboardReview{}, operatorPlatformError(actionCreateReview, err)
	}
	return tenantDashboardReview(review), nil
}

// CloseReview closes a review without merging it, mirroring `erun review
// close` and the MCP review_close tool: always a transition to CLOSED, not a
// general status setter.
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

	review, err := client.UpdateReviewStatus(requestCtx, reviewID, eruncommon.PlatformUpdateReviewStatusParams{Status: "CLOSED"})
	if err != nil {
		return uiTenantDashboardReview{}, operatorPlatformError(actionCloseReview, err)
	}
	return tenantDashboardReview(review), nil
}

// AdvanceMergeQueue advances targetBranch's merge queue head to MERGE. When
// the queue head still has unresolved comment threads, the platform refuses
// and this reports the block (which review, how many threads) on the same
// review shape rather than a bare error string — Blocked/UnresolvedThreads
// let the caller route the operator to the threads, or to
// OverrideAdvanceMergeQueue, instead of hitting a dead end.
func (a *App) AdvanceMergeQueue(input uiAdvanceMergeQueueInput) (uiTenantDashboardReview, error) {
	tenant, err := requireTenant("advancing the merge queue", input.Tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	targetBranch := strings.TrimSpace(input.TargetBranch)
	if targetBranch == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("target branch is required")
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

	review, err := client.AdvanceMergeQueue(requestCtx, targetBranch)
	if err != nil {
		var blocked *eruncommon.PlatformMergeQueueBlockedError
		if errors.As(err, &blocked) {
			unresolved := blocked.UnresolvedThreads
			return uiTenantDashboardReview{ReviewID: blocked.ReviewID, Blocked: true, UnresolvedThreads: &unresolved}, nil
		}
		return uiTenantDashboardReview{}, operatorPlatformError(actionAdvanceQueue, err)
	}
	return tenantDashboardReview(review), nil
}

// OverrideAdvanceMergeQueue bypasses AdvanceMergeQueue's unresolved-thread
// gate. Reason is required — the backend refuses a blank one — and is
// recorded in the platform's audit trail alongside the caller's identity.
func (a *App) OverrideAdvanceMergeQueue(input uiOverrideAdvanceMergeQueueInput) (uiTenantDashboardReview, error) {
	tenant, err := requireTenant("overriding the merge queue's unresolved-thread gate", input.Tenant)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	targetBranch := strings.TrimSpace(input.TargetBranch)
	if targetBranch == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("target branch is required")
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

	review, err := client.OverrideAdvanceMergeQueue(requestCtx, targetBranch, reason)
	if err != nil {
		return uiTenantDashboardReview{}, operatorPlatformError(actionOverrideAdvanceQueue, err)
	}
	return tenantDashboardReview(review), nil
}

// validateCreateReviewCommentInput checks CreateReviewComment's already-
// trimmed input, kept apart from the call itself so that function's own
// branching stays under the module's cyclomatic-complexity cap.
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

// CreateReviewComment starts a new top-level thread anchored to a diff line —
// as opposed to CreateReviewReply, which replies within an existing thread.
// tenant_review_detail.go's ParentCommentID-required check stays as the
// reply path's own contract; this is the sibling entry point for opening a
// thread the diff panel's line click provides the anchor for.
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

	comment, err := client.CreateComment(requestCtx, reviewID, eruncommon.PlatformCreateCommentParams{
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

// ResolveReviewComment marks a comment thread CLOSED (resolved). The
// dashboard only ever offers this on a thread's root comment — never a reply —
// so, unlike the CLI/MCP paths, there is no reply-addressed-to-root rejection
// to surface here.
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
	comment, err := client.UpdateCommentStatus(requestCtx, reviewID, commentID, eruncommon.PlatformUpdateCommentStatusParams{Status: status})
	if err != nil {
		return uiReviewComment{}, operatorPlatformError(action, err)
	}
	return tenantDashboardComment(comment), nil
}
