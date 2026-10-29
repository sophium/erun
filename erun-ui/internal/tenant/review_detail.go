package tenant

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	reviewpkg "github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// LoadReviewDetail loads one review's detail; each sub-read degrades independently.
func (s *Service) LoadReviewDetail(input ReviewDetailInput) (ReviewDetail, error) {
	tenant, err := uimodel.RequireTenant("loading review detail", input.Tenant)
	if err != nil {
		return ReviewDetail{}, err
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return ReviewDetail{}, fmt.Errorf("review id is required")
	}

	detail := ReviewDetail{ReviewID: reviewID}
	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
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

	if restricted := RestrictedRead(capabilities, tenantDashboardReadReview); restricted != "" {
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
	detail.CanComment = RestrictedRead(capabilities, tenantDashboardWriteComment) == ""
	detail.CanClose = RestrictedRead(capabilities, tenantDashboardWriteReviewStatus) == ""
	detail.CanResolveComments = RestrictedRead(capabilities, tenantDashboardWriteCommentStatus) == ""
	detail.CanAssignReviewers = RestrictedRead(capabilities, tenantDashboardWriteReviewers) == ""
	detail.CanRemoveReviewers = RestrictedRead(capabilities, tenantDashboardRemoveReviewers) == ""
	if detail.CanAssignReviewers {
		detail.AvailableReviewers = tenantDashboardEnrolledReviewerChoices(requestCtx, client, capabilities)
	}
	detail.AccessRemedies = loadAccessRemedies(requestCtx, client, capabilities, whoami.UserID,
		detail.CommentsRestricted, detail.BuildsRestricted, detail.ReviewersRestricted)
	return detail, nil
}

// loadReviewDetailReviewers degrades independently of the rest of the detail.
func loadReviewDetailReviewers(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, reviewID string, usernames map[string]string, detail *ReviewDetail) {
	if restricted := RestrictedRead(capabilities, tenantDashboardReadReviewers); restricted != "" {
		detail.ReviewersRestricted = restricted
		return
	}
	reviewers, err := client.ListReviewers(ctx, reviewID)
	if err != nil {
		detail.ReviewersError = tenantDashboardReadError(tenantDashboardReadReviewers, err)
		return
	}
	converted := make([]Reviewer, 0, len(reviewers))
	for _, reviewer := range reviewers {
		converted = append(converted, Reviewer{UserID: reviewer.UserID, Username: usernames[reviewer.UserID]})
	}
	detail.Reviewers = converted
}

// tenantDashboardEnrolledReviewerChoices is best effort: nil leaves the picker empty
// rather than failing the detail.
func tenantDashboardEnrolledReviewerChoices(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities) []Reviewer {
	if RestrictedRead(capabilities, tenantDashboardReadUsers) != "" {
		return nil
	}
	users, err := client.ListUsers(ctx, model.PlatformListUsersParams{})
	if err != nil {
		return nil
	}
	choices := make([]Reviewer, 0, len(users))
	for _, user := range users {
		choices = append(choices, Reviewer{UserID: user.UserID, Username: user.Username})
	}
	return choices
}

func loadReviewDetailComments(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, reviewID string, usernames map[string]string, detail *ReviewDetail) {
	if restricted := RestrictedRead(capabilities, ReadComments); restricted != "" {
		detail.CommentsRestricted = restricted
		return
	}
	comments, err := client.ListComments(ctx, reviewID)
	if err != nil {
		detail.CommentsError = tenantDashboardReadError(ReadComments, err)
		return
	}
	detail.Comments = tenantDashboardComments(comments, usernames)
	detail.UnresolvedThreads = reviewpkg.CountUnresolvedThreads(comments)
}

func loadReviewDetailBuilds(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, reviewID, reviewName string, detail *ReviewDetail) {
	if restricted := RestrictedRead(capabilities, tenantDashboardReadBuilds); restricted != "" {
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
func loadReviewDetailQueuePosition(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, review model.PlatformReview, reviewID string) int {
	if RestrictedRead(capabilities, ReadMergeQueue) != "" {
		return 0
	}
	queue, err := client.ListMergeQueue(ctx, model.PlatformMergeQueueParams{
		Repository: review.Repository, TargetBranch: review.TargetBranch,
	})
	if err != nil {
		return 0
	}
	return QueuePosition(queue, reviewID)
}

// CreateReviewReply returns a real error so the frontend keeps the draft until it
// succeeds.
func (s *Service) CreateReviewReply(input CreateReviewReplyInput) (ReviewComment, error) {
	tenant, err := uimodel.RequireTenant("replying to a review comment", input.Tenant)
	if err != nil {
		return ReviewComment{}, err
	}
	reviewID := strings.TrimSpace(input.ReviewID)
	if reviewID == "" {
		return ReviewComment{}, fmt.Errorf("review id is required")
	}
	parentCommentID := strings.TrimSpace(input.ParentCommentID)
	if parentCommentID == "" {
		return ReviewComment{}, fmt.Errorf("parent comment id is required")
	}
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return ReviewComment{}, fmt.Errorf("reply body is required")
	}

	ctx := s.d.Context()
	client, requestCtx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return ReviewComment{}, err
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
		return ReviewComment{}, operatorPlatformError(actionCommentReview, err)
	}
	return tenantDashboardComment(comment), nil
}

func tenantDashboardBuildsForReview(builds []model.PlatformBuild, reviewName string) []DashboardBuild {
	converted := make([]DashboardBuild, 0, len(builds))
	for _, build := range builds {
		converted = append(converted, DashboardBuild{
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

func tenantDashboardComments(comments []model.PlatformComment, usernames map[string]string) []ReviewComment {
	converted := make([]ReviewComment, 0, len(comments))
	for _, comment := range comments {
		converted = append(converted, tenantDashboardCommentWithUsername(comment, usernames))
	}
	return converted
}

func tenantDashboardCommentWithUsername(comment model.PlatformComment, usernames map[string]string) ReviewComment {
	converted := tenantDashboardComment(comment)
	converted.CreatorUsername = usernames[comment.CreatorUserID]
	return converted
}

func tenantDashboardComment(comment model.PlatformComment) ReviewComment {
	return ReviewComment{
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

// QueuePosition returns the 1-based position, or 0 when not queued.
func QueuePosition(queue []model.PlatformReview, reviewID string) int {
	for i, review := range queue {
		if review.ReviewID == reviewID {
			return i + 1
		}
	}
	return 0
}
