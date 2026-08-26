package main

import (
	"context"
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
)

// CreateReview opens a review. It is a real, immediate write — there is no
// dashboard preview path, matching every other side-effecting action here.
func (a *App) CreateReview(input uiCreateReviewInput) (uiTenantDashboardReview, error) {
	tenant := strings.TrimSpace(input.Tenant)
	if tenant == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("tenant is required")
	}
	apiURL := strings.TrimSpace(input.APIURL)
	if apiURL == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("tenant API URL is required")
	}
	alias := strings.TrimSpace(input.CloudProviderAlias)
	if alias == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("tenant primary cloud alias is required")
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
	client, requestCtx, cancel, err := a.tenantDashboardBearerClient(ctx, apiURL, alias)
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
	tenant := strings.TrimSpace(input.Tenant)
	if tenant == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("tenant is required")
	}
	apiURL := strings.TrimSpace(input.APIURL)
	if apiURL == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("tenant API URL is required")
	}
	alias := strings.TrimSpace(input.CloudProviderAlias)
	if alias == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("tenant primary cloud alias is required")
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("review id is required")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantDashboardBearerClient(ctx, apiURL, alias)
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

// AdvanceMergeQueue advances targetBranch's merge queue head to MERGED.
func (a *App) AdvanceMergeQueue(input uiAdvanceMergeQueueInput) (uiTenantDashboardReview, error) {
	tenant := strings.TrimSpace(input.Tenant)
	if tenant == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("tenant is required")
	}
	apiURL := strings.TrimSpace(input.APIURL)
	if apiURL == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("tenant API URL is required")
	}
	alias := strings.TrimSpace(input.CloudProviderAlias)
	if alias == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("tenant primary cloud alias is required")
	}
	targetBranch := strings.TrimSpace(input.TargetBranch)
	if targetBranch == "" {
		return uiTenantDashboardReview{}, fmt.Errorf("target branch is required")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantDashboardBearerClient(ctx, apiURL, alias)
	if err != nil {
		return uiTenantDashboardReview{}, err
	}
	defer cancel()

	review, err := client.AdvanceMergeQueue(requestCtx, targetBranch)
	if err != nil {
		return uiTenantDashboardReview{}, operatorPlatformError(actionAdvanceQueue, err)
	}
	return tenantDashboardReview(review), nil
}

// validateCreateReviewCommentInput checks CreateReviewComment's already-
// trimmed input, kept apart from the call itself so that function's own
// branching stays under the module's cyclomatic-complexity cap.
func validateCreateReviewCommentInput(tenant, apiURL, alias, reviewID, commitID, filePath, body string) error {
	if strings.TrimSpace(tenant) == "" {
		return fmt.Errorf("tenant is required")
	}
	if apiURL == "" {
		return fmt.Errorf("tenant API URL is required")
	}
	if alias == "" {
		return fmt.Errorf("tenant primary cloud alias is required")
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
	apiURL := strings.TrimSpace(input.APIURL)
	alias := strings.TrimSpace(input.CloudProviderAlias)
	reviewID := strings.TrimSpace(input.ReviewID)
	commitID := strings.TrimSpace(input.CommitID)
	filePath := strings.TrimSpace(input.FilePath)
	body := strings.TrimSpace(input.Body)
	if err := validateCreateReviewCommentInput(input.Tenant, apiURL, alias, reviewID, commitID, filePath, body); err != nil {
		return uiReviewComment{}, err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantDashboardBearerClient(ctx, apiURL, alias)
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
