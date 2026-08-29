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
	tenant, err := requireTenant("loading review detail", input.Tenant)
	if err != nil {
		return uiReviewDetail{}, err
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
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		detail.APIError = err.Error()
		return detail, nil
	}
	defer cancel()

	whoami, err := client.Whoami(requestCtx)
	if err != nil {
		_, detail.APIError = tenantDashboardIdentityFailure(err)
		return detail, nil
	}
	capabilities := whoami.Capabilities
	usernames := tenantDashboardUsernames(requestCtx, client, capabilities)

	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadReview); restricted != "" {
		detail.Restricted = restricted
		return detail, nil
	}
	review, err := client.GetReview(requestCtx, reviewID)
	if err != nil {
		detail.Error = tenantDashboardReadError(tenantDashboardReadReview, err)
		return detail, nil
	}
	converted := tenantDashboardReviewWithUsername(review, usernames)
	detail.Review = &converted

	loadReviewDetailComments(requestCtx, client, capabilities, reviewID, usernames, &detail)
	loadReviewDetailBuilds(requestCtx, client, capabilities, reviewID, review.Name, &detail)
	loadReviewDetailReviewers(requestCtx, client, capabilities, reviewID, usernames, &detail)
	detail.QueuePosition = loadReviewDetailQueuePosition(requestCtx, client, capabilities, review, reviewID)
	detail.CanComment = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteComment) == ""
	detail.CanClose = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteReviewStatus) == ""
	detail.CanResolveComments = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteCommentStatus) == ""
	detail.CanAssignReviewers = restrictedTenantDashboardRead(capabilities, tenantDashboardWriteReviewers) == ""
	detail.CanRemoveReviewers = restrictedTenantDashboardRead(capabilities, tenantDashboardRemoveReviewers) == ""
	if detail.CanAssignReviewers {
		detail.AvailableReviewers = tenantDashboardEnrolledReviewerChoices(requestCtx, client, capabilities)
	}
	return detail, nil
}

// loadReviewDetailReviewers loads a review's assigned reviewers, degrading
// independently like loadReviewDetailComments/loadReviewDetailBuilds: a
// caller who cannot read reviewers still sees the rest of the detail.
func loadReviewDetailReviewers(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, reviewID string, usernames map[string]string, detail *uiReviewDetail) {
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadReviewers); restricted != "" {
		detail.ReviewersRestricted = restricted
		return
	}
	reviewers, err := client.ListReviewers(ctx, reviewID)
	if err != nil {
		detail.ReviewersError = tenantDashboardReadError(tenantDashboardReadReviewers, err)
		return
	}
	converted := make([]uiReviewer, 0, len(reviewers))
	for _, reviewer := range reviewers {
		converted = append(converted, uiReviewer{UserID: reviewer.UserID, Username: usernames[reviewer.UserID]})
	}
	detail.Reviewers = converted
}

// tenantDashboardEnrolledReviewerChoices lists the tenant's enrolled users for
// the Add reviewers picker, best effort: a caller who cannot read /v1/users,
// or a read that fails, gets back nil rather than failing the whole detail —
// the Add reviewers action itself simply has nothing to offer.
func tenantDashboardEnrolledReviewerChoices(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities) []uiReviewer {
	if restrictedTenantDashboardRead(capabilities, tenantDashboardReadUsers) != "" {
		return nil
	}
	users, err := client.ListUsers(ctx, eruncommon.PlatformListUsersParams{})
	if err != nil {
		return nil
	}
	choices := make([]uiReviewer, 0, len(users))
	for _, user := range users {
		choices = append(choices, uiReviewer{UserID: user.UserID, Username: user.Username})
	}
	return choices
}

func loadReviewDetailComments(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, reviewID string, usernames map[string]string, detail *uiReviewDetail) {
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadComments); restricted != "" {
		detail.CommentsRestricted = restricted
		return
	}
	comments, err := client.ListComments(ctx, reviewID)
	if err != nil {
		detail.CommentsError = tenantDashboardReadError(tenantDashboardReadComments, err)
		return
	}
	detail.Comments = tenantDashboardComments(comments, usernames)
	detail.UnresolvedThreads = eruncommon.CountUnresolvedThreads(comments)
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
	tenant, err := requireTenant("replying to a review comment", input.Tenant)
	if err != nil {
		return uiReviewComment{}, err
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
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
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
		return uiReviewComment{}, operatorPlatformError(actionCommentReview, err)
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

func tenantDashboardComments(comments []eruncommon.PlatformComment, usernames map[string]string) []uiReviewComment {
	converted := make([]uiReviewComment, 0, len(comments))
	for _, comment := range comments {
		converted = append(converted, tenantDashboardCommentWithUsername(comment, usernames))
	}
	return converted
}

func tenantDashboardCommentWithUsername(comment eruncommon.PlatformComment, usernames map[string]string) uiReviewComment {
	converted := tenantDashboardComment(comment)
	converted.CreatorUsername = usernames[comment.CreatorUserID]
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
