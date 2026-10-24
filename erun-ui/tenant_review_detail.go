package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// LoadReviewDetail loads one review's detail; each sub-read degrades independently.
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
		detail.AccessRemedies = loadAccessRemedies(requestCtx, client, capabilities, whoami.UserID, restricted)
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
	detail.AccessRemedies = loadAccessRemedies(requestCtx, client, capabilities, whoami.UserID,
		detail.CommentsRestricted, detail.BuildsRestricted, detail.ReviewersRestricted)
	return detail, nil
}

// loadReviewDetailReviewers degrades independently of the rest of the detail.
func loadReviewDetailReviewers(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, reviewID string, usernames map[string]string, detail *uiReviewDetail) {
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

// tenantDashboardEnrolledReviewerChoices is best effort: nil leaves the picker empty
// rather than failing the detail.
func tenantDashboardEnrolledReviewerChoices(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities) []uiReviewer {
	if restrictedTenantDashboardRead(capabilities, tenantDashboardReadUsers) != "" {
		return nil
	}
	users, err := client.ListUsers(ctx, platformclient.PlatformListUsersParams{})
	if err != nil {
		return nil
	}
	choices := make([]uiReviewer, 0, len(users))
	for _, user := range users {
		choices = append(choices, uiReviewer{UserID: user.UserID, Username: user.Username})
	}
	return choices
}

func loadReviewDetailComments(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, reviewID string, usernames map[string]string, detail *uiReviewDetail) {
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
	detail.UnresolvedThreads = reviewpkg.CountUnresolvedThreads(comments)
}

func loadReviewDetailBuilds(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, reviewID, reviewName string, detail *uiReviewDetail) {
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

// loadReviewDetailQueuePosition reports 0 on denial or failure: the position is
// supplemental.
func loadReviewDetailQueuePosition(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, review platformclient.PlatformReview, reviewID string) int {
	if restrictedTenantDashboardRead(capabilities, tenantDashboardReadMergeQueue) != "" {
		return 0
	}
	queue, err := client.ListMergeQueue(ctx, platformclient.PlatformMergeQueueParams{
		Repository: review.Repository, TargetBranch: review.TargetBranch,
	})
	if err != nil {
		return 0
	}
	return reviewQueuePosition(queue, reviewID)
}

// CreateReviewReply returns a real error so the frontend keeps the draft until it
// succeeds.
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

	comment, err := client.CreateComment(requestCtx, reviewID, platformclient.PlatformCreateCommentParams{
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

func tenantDashboardBuildsForReview(builds []platformclient.PlatformBuild, reviewName string) []uiTenantDashboardBuild {
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
			Profile:    build.Profile,
			CreatedAt:  tenantDashboardTime(build.CreatedAt),
			UpdatedAt:  tenantDashboardTime(build.UpdatedAt),
		})
	}
	return converted
}

func tenantDashboardComments(comments []platformclient.PlatformComment, usernames map[string]string) []uiReviewComment {
	converted := make([]uiReviewComment, 0, len(comments))
	for _, comment := range comments {
		converted = append(converted, tenantDashboardCommentWithUsername(comment, usernames))
	}
	return converted
}

func tenantDashboardCommentWithUsername(comment platformclient.PlatformComment, usernames map[string]string) uiReviewComment {
	converted := tenantDashboardComment(comment)
	converted.CreatorUsername = usernames[comment.CreatorUserID]
	return converted
}

func tenantDashboardComment(comment platformclient.PlatformComment) uiReviewComment {
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

// reviewQueuePosition returns the 1-based position, or 0 when not queued.
func reviewQueuePosition(queue []platformclient.PlatformReview, reviewID string) int {
	for i, review := range queue {
		if review.ReviewID == reviewID {
			return i + 1
		}
	}
	return 0
}
