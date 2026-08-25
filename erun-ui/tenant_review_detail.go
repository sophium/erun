package main

import (
	"context"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// tenant_review_detail.go extends the tenant dashboard's Reviews tab with a
// single review's own detail — its comment threads, recorded builds, and its
// position in its target branch's merge queue — plus replying to an existing
// comment thread. Loaded lazily, one review at a time, from a row the
// operator opens in the Reviews panel (#1199).

// LoadReviewDetail loads one review's detail. Each sub-read is gated and
// degrades independently, mirroring LoadTenantDashboard's own panels: a
// caller who cannot read comments still sees the review and its builds.
func (a *App) LoadReviewDetail(input uiReviewDetailInput) (uiReviewDetail, error) {
	tenant := strings.TrimSpace(input.Tenant)
	if tenant == "" {
		return uiReviewDetail{}, fmt.Errorf("tenant is required")
	}
	apiURL := strings.TrimSpace(input.APIURL)
	if apiURL == "" {
		return uiReviewDetail{}, fmt.Errorf("tenant API URL is required")
	}
	alias := strings.TrimSpace(input.CloudProviderAlias)
	if alias == "" {
		return uiReviewDetail{}, fmt.Errorf("tenant primary cloud alias is required")
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return uiReviewDetail{}, fmt.Errorf("review id is required")
	}

	detail := uiReviewDetail{ReviewID: reviewID}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, requestCtx, cancel, err := a.tenantDashboardBearerClient(ctx, apiURL, alias)
	if err != nil {
		detail.APIError = err.Error()
		return detail, nil
	}
	defer cancel()

	whoami, err := client.Whoami(requestCtx)
	if err != nil {
		detail.APIError = tenantDashboardIdentityError(err)
		return detail, nil
	}
	capabilities := whoami.Capabilities

	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadReview); restricted != "" {
		detail.Restricted = restricted
		return detail, nil
	}
	review, err := client.GetReview(requestCtx, reviewID)
	if err != nil {
		detail.Error = tenantDashboardReadError(tenantDashboardReadReview, err)
		return detail, nil
	}
	converted := tenantDashboardReview(review)
	detail.Review = &converted

	loadReviewDetailComments(requestCtx, client, capabilities, reviewID, &detail)
	loadReviewDetailBuilds(requestCtx, client, capabilities, reviewID, review.Name, &detail)
	detail.QueuePosition = loadReviewDetailQueuePosition(requestCtx, client, capabilities, review, reviewID)
	detail.CanComment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteComment) == ""
	detail.CanClose = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteReviewStatus) == ""
	return detail, nil
}

func loadReviewDetailComments(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, reviewID string, detail *uiReviewDetail) {
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadComments); restricted != "" {
		detail.CommentsRestricted = restricted
		return
	}
	comments, err := client.ListComments(ctx, reviewID)
	if err != nil {
		detail.CommentsError = tenantDashboardReadError(tenantDashboardReadComments, err)
		return
	}
	detail.Comments = tenantDashboardComments(comments)
}

func loadReviewDetailBuilds(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, reviewID, reviewName string, detail *uiReviewDetail) {
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadBuilds); restricted != "" {
		detail.BuildsRestricted = restricted
		return
	}
	builds, err := client.ListBuilds(ctx, reviewID)
	if err != nil {
		detail.BuildsError = tenantDashboardReadError(tenantDashboardReadBuilds, err)
		return
	}
	detail.Builds = tenantDashboardBuildsForReview(builds, reviewName)
}

// loadReviewDetailQueuePosition reports 0 (not queued) both when the caller
// may not read the queue and when the read itself fails — the position is
// supplemental detail in the dialog header, not something worth its own
// error line the way comments and builds get.
func loadReviewDetailQueuePosition(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, review eruncommon.PlatformReview, reviewID string) int {
	if restrictedTenantDashboardRead(capabilities, tenantDashboardReadMergeQueue) != "" {
		return 0
	}
	queue, err := client.ListMergeQueue(ctx, review.TargetBranch)
	if err != nil {
		return 0
	}
	return reviewQueuePosition(queue, reviewID)
}

// CreateReviewReply posts a reply in an existing comment thread. It is a
// direct write, not a dashboard panel read, so a failure is a real Go error
// the frontend's submit handler catches — matching the "submit failed with
// the text preserved" contract: the caller keeps the draft body on error and
// only clears it once this call actually succeeds.
func (a *App) CreateReviewReply(input uiCreateReviewReplyInput) (uiReviewComment, error) {
	tenant := strings.TrimSpace(input.Tenant)
	if tenant == "" {
		return uiReviewComment{}, fmt.Errorf("tenant is required")
	}
	apiURL := strings.TrimSpace(input.APIURL)
	if apiURL == "" {
		return uiReviewComment{}, fmt.Errorf("tenant API URL is required")
	}
	alias := strings.TrimSpace(input.CloudProviderAlias)
	if alias == "" {
		return uiReviewComment{}, fmt.Errorf("tenant primary cloud alias is required")
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return uiReviewComment{}, fmt.Errorf("review id is required")
	}
	parentCommentID := strings.TrimSpace(input.ParentCommentID)
	if parentCommentID == "" {
		return uiReviewComment{}, fmt.Errorf("parent comment id is required")
	}
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return uiReviewComment{}, fmt.Errorf("reply body is required")
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
		CommitID:        input.CommitID,
		FilePath:        input.FilePath,
		Line:            input.Line,
		Body:            body,
		ParentCommentID: parentCommentID,
	})
	if err != nil {
		return uiReviewComment{}, err
	}
	return tenantDashboardComment(comment), nil
}

func tenantDashboardBuildsForReview(builds []eruncommon.PlatformBuild, reviewName string) []uiTenantDashboardBuild {
	converted := make([]uiTenantDashboardBuild, 0, len(builds))
	for _, build := range builds {
		converted = append(converted, uiTenantDashboardBuild{
			BuildID:    build.BuildID,
			TenantID:   build.TenantID,
			ReviewID:   build.ReviewID,
			ReviewName: strings.TrimSpace(reviewName),
			Successful: build.Successful,
			CommitID:   build.CommitID,
			Version:    build.Version,
			CreatedAt:  tenantDashboardTime(build.CreatedAt),
			UpdatedAt:  tenantDashboardTime(build.UpdatedAt),
		})
	}
	return converted
}

func tenantDashboardComments(comments []eruncommon.PlatformComment) []uiReviewComment {
	converted := make([]uiReviewComment, 0, len(comments))
	for _, comment := range comments {
		converted = append(converted, tenantDashboardComment(comment))
	}
	return converted
}

func tenantDashboardComment(comment eruncommon.PlatformComment) uiReviewComment {
	return uiReviewComment{
		CommentID:       comment.CommentID,
		CreatorUserID:   comment.CreatorUserID,
		Status:          comment.Status,
		ParentCommentID: comment.ParentCommentID,
		CommitID:        comment.CommitID,
		FilePath:        comment.FilePath,
		Line:            comment.Line,
		Body:            comment.Body,
		CreatedAt:       tenantDashboardTime(comment.CreatedAt),
	}
}

// reviewQueuePosition reports reviewID's 1-based position in queue, or 0 when
// it is not present — the review is not currently queued for its target
// branch.
func reviewQueuePosition(queue []eruncommon.PlatformReview, reviewID string) int {
	for i, review := range queue {
		if review.ReviewID == reviewID {
			return i + 1
		}
	}
	return 0
}
