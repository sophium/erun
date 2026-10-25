package review

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/platformapi"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/build/classify"
)

// RunReviewList lists reviews visible to the caller's tenant.
func RunReviewList(ctx core.Context, store config.CloudReadStore, alias string, params model.ReviewListParams, deps cloud.CloudDependencies) ([]model.PlatformReview, error) {
	// Validate filters before the alias lookup so a bad argument never depends on
	// the platform being reachable.
	status, err := core.NormalizeReviewStatus(params.Status)
	if err != nil {
		return nil, err
	}
	params.Status = status
	// Canonical form lets an SSH spelling match reviews recorded over HTTPS.
	if strings.TrimSpace(params.Repository) != "" {
		repository, err := core.RepositoryIdentity(params.Repository)
		if err != nil {
			return nil, err
		}
		params.Repository = repository
	}
	if err := validateReviewListParams(params); err != nil {
		return nil, err
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	filter := reviewListFilter(params)
	traceReviewList(ctx, provider, filter, params)
	if ctx.DryRun {
		return nil, nil
	}
	if err := resolveReviewListCallerFilters(client, params, &filter); err != nil {
		return nil, err
	}
	return client.ListReviews(context.Background(), filter)
}

func validateReviewListParams(params model.ReviewListParams) error {
	if params.Mine && strings.TrimSpace(params.AuthorUserID) != "" {
		return fmt.Errorf("cannot combine --mine with an explicit author user id")
	}
	if params.WaitingOnMe && strings.TrimSpace(params.ReviewerUserID) != "" {
		return fmt.Errorf("cannot combine --waiting-on-me with an explicit reviewer user id")
	}
	return nil
}

func reviewListFilter(params model.ReviewListParams) platformclient.PlatformReviewFilter {
	return platformclient.PlatformReviewFilter{
		Repository:     params.Repository,
		TargetBranch:   params.TargetBranch,
		SourceBranch:   params.SourceBranch,
		Status:         params.Status,
		AuthorUserID:   params.AuthorUserID,
		ReviewerUserID: params.ReviewerUserID,
	}
}

// traceReviewList includes the whoami lookup so dry-run traces match real runs.
func traceReviewList(ctx core.Context, provider model.CloudProviderConfig, filter platformclient.PlatformReviewFilter, params model.ReviewListParams) {
	details := platformclient.ReviewFilterTraceDetails(filter)
	if params.Mine {
		details = append(details, "authorUserId=<caller>")
	}
	if params.WaitingOnMe {
		details = append(details, "reviewerUserId=<caller>")
	}
	if params.Mine || params.WaitingOnMe {
		platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/whoami")
	}
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/reviews", details...)
}

func resolveReviewListCallerFilters(client *platformclient.PlatformClient, params model.ReviewListParams, filter *platformclient.PlatformReviewFilter) error {
	if !params.Mine && !params.WaitingOnMe {
		return nil
	}
	whoami, err := client.Whoami(context.Background())
	if err != nil {
		return fmt.Errorf("resolve caller identity for --mine/--waiting-on-me: %w", err)
	}
	if params.Mine {
		filter.AuthorUserID = whoami.UserID
	}
	if params.WaitingOnMe {
		filter.ReviewerUserID = whoami.UserID
	}
	return nil
}

// RunReviewShow fetches one review together with its comments and builds.
func RunReviewShow(ctx core.Context, store config.CloudReadStore, alias, reviewID string, deps cloud.CloudDependencies) (model.ReviewDetail, error) {
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.ReviewDetail{}, err
	}
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID)
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID+"/comments")
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID+"/builds")
	if ctx.DryRun {
		return model.ReviewDetail{}, nil
	}
	review, err := client.GetReview(context.Background(), reviewID)
	if err != nil {
		return model.ReviewDetail{}, err
	}
	comments, err := client.ListComments(context.Background(), reviewID)
	if err != nil {
		return model.ReviewDetail{}, err
	}
	builds, err := client.ListBuilds(context.Background(), reviewID)
	if err != nil {
		return model.ReviewDetail{}, err
	}
	return model.ReviewDetail{Review: review, Comments: comments, Builds: builds, UnresolvedThreads: CountUnresolvedThreads(comments)}, nil
}

// CountUnresolvedThreads counts OPEN root comments; replies carry no status
// (enforced by comments_validate in erun-backend-db).
func CountUnresolvedThreads(comments []model.PlatformComment) int {
	count := 0
	for _, comment := range comments {
		if strings.TrimSpace(comment.ParentCommentID) == "" && comment.Status == "OPEN" {
			count++
		}
	}
	return count
}

// threadRootCommentID returns the root for a reply so the caller can retry
// against it; an unknown id is left to the backend's not-found.
func threadRootCommentID(comments []model.PlatformComment, commentID string) (string, bool) {
	for _, comment := range comments {
		if comment.CommentID != commentID {
			continue
		}
		if root := strings.TrimSpace(comment.ParentCommentID); root != "" {
			return root, true
		}
		return "", false
	}
	return "", false
}

// runReviewCommentStatus refuses a reply id: only a thread root is addressable.
func runReviewCommentStatus(ctx core.Context, store config.CloudReadStore, alias, reviewID, commentID, status string, deps cloud.CloudDependencies) (model.PlatformComment, error) {
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformComment{}, err
	}
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID+"/comments")
	platformapi.TracePlatformCall(ctx, provider, "PATCH", "/v1/reviews/"+reviewID+"/comments/"+commentID+"/status", "status="+status)
	if ctx.DryRun {
		return model.PlatformComment{}, nil
	}
	comments, err := client.ListComments(context.Background(), reviewID)
	if err != nil {
		return model.PlatformComment{}, err
	}
	if rootID, isReply := threadRootCommentID(comments, commentID); isReply {
		return model.PlatformComment{}, fmt.Errorf("comment %s is a reply; only a thread's root comment can be resolved or unresolved — retry against root comment %s", commentID, rootID)
	}
	return client.UpdateCommentStatus(context.Background(), reviewID, commentID, platformclient.PlatformUpdateCommentStatusParams{Status: status})
}

// RunReviewResolve marks a root comment's thread CLOSED.
func RunReviewResolve(ctx core.Context, store config.CloudReadStore, alias, reviewID, commentID string, deps cloud.CloudDependencies) (model.PlatformComment, error) {
	return runReviewCommentStatus(ctx, store, alias, reviewID, commentID, "CLOSED", deps)
}

// RunReviewUnresolve reopens a root comment's thread.
func RunReviewUnresolve(ctx core.Context, store config.CloudReadStore, alias, reviewID, commentID string, deps cloud.CloudDependencies) (model.PlatformComment, error) {
	return runReviewCommentStatus(ctx, store, alias, reviewID, commentID, "OPEN", deps)
}

// RunReviewCreate opens a review recording its repository, so a multi-repo
// tenant can tell reviews apart.
func RunReviewCreate(ctx core.Context, store config.CloudReadStore, alias string, params model.PlatformCreateReviewParams, deps cloud.CloudDependencies) (model.PlatformReview, error) {
	repository, err := ResolveReviewRepository(ctx, params.Repository, "review create")
	if err != nil {
		return model.PlatformReview{}, err
	}
	params.Repository = repository
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformReview{}, err
	}
	details := []string{
		"repository=" + params.Repository,
		"name=" + params.Name,
		"targetBranch=" + params.TargetBranch,
		"sourceBranch=" + params.SourceBranch,
	}
	if strings.TrimSpace(params.IssueRef) != "" {
		details = append(details, "issueRef="+strings.TrimSpace(params.IssueRef))
	}
	platformapi.TracePlatformCall(ctx, provider, "POST", "/v1/reviews", details...)
	if ctx.DryRun {
		return model.PlatformReview{}, nil
	}
	return client.CreateReview(context.Background(), params)
}

// RunReviewComment posts a comment (or a reply) on a review.
func RunReviewComment(ctx core.Context, store config.CloudReadStore, alias string, params model.ReviewCommentParams, deps cloud.CloudDependencies) (model.PlatformComment, error) {
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformComment{}, err
	}
	details := []string{
		"commitId=" + params.CommitID,
		"filePath=" + params.FilePath,
		"line=" + platformclient.FormatCommentLine(params.Line),
	}
	if strings.TrimSpace(params.ParentCommentID) != "" {
		details = append(details, "replyTo="+params.ParentCommentID)
	}
	platformapi.TracePlatformCall(ctx, provider, "POST", "/v1/reviews/"+params.ReviewID+"/comments", details...)
	if ctx.DryRun {
		return model.PlatformComment{}, nil
	}
	return client.CreateComment(context.Background(), params.ReviewID, platformclient.PlatformCreateCommentParams{
		CommitID:        params.CommitID,
		FilePath:        params.FilePath,
		Line:            params.Line,
		Body:            params.Body,
		ParentCommentID: params.ParentCommentID,
	})
}

// RunReviewClose closes a review.
func RunReviewClose(ctx core.Context, store config.CloudReadStore, alias, reviewID string, deps cloud.CloudDependencies) (model.PlatformReview, error) {
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformReview{}, err
	}
	platformapi.TracePlatformCall(ctx, provider, "PATCH", "/v1/reviews/"+reviewID+"/status", "status=CLOSED")
	if ctx.DryRun {
		return model.PlatformReview{}, nil
	}
	return client.UpdateReviewStatus(context.Background(), reviewID, platformclient.PlatformUpdateReviewStatusParams{Status: "CLOSED"})
}

// RunReviewRecordBuild records a build; the backend moves the review to READY
// or FAILED and promotes a queue head to MERGE. There is no separate status call.
func RunReviewRecordBuild(ctx core.Context, store config.CloudReadStore, alias string, params model.ReviewRecordBuildParams, deps cloud.CloudDependencies) (model.PlatformBuild, error) {
	if strings.TrimSpace(params.Version) == "" {
		return model.PlatformBuild{}, fmt.Errorf("version is required: record the version the build minted")
	}
	if err := classify.EnsureNotKnownInfrastructureBuildFailure(params.Successful, params.FailureDetail); err != nil {
		return model.PlatformBuild{}, err
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformBuild{}, err
	}
	details := []string{
		"commitId=" + params.CommitID,
		"version=" + params.Version,
	}
	details = append(details, "successful="+strconv.FormatBool(params.Successful))
	if strings.TrimSpace(params.FailureDetail) != "" {
		details = append(details, "failureDetail="+params.FailureDetail)
	}
	platformapi.TracePlatformCall(ctx, provider, "POST", "/v1/reviews/"+params.ReviewID+"/builds", details...)
	if ctx.DryRun {
		return model.PlatformBuild{}, nil
	}
	return client.CreateBuild(context.Background(), params.ReviewID, platformclient.PlatformCreateBuildParams{
		CommitID:      params.CommitID,
		Kind:          "RECORDED",
		Version:       params.Version,
		Successful:    params.Successful,
		FailureDetail: params.FailureDetail,
	})
}

// RunReviewReportMerged reports a review MERGED; the platform verifies against
// remoteURL either way. A review at MERGE needs buildID, the merge commit's
// successful build; any other review omits it and is checked for its branch's
// content in the target. Failure is MERGE_NOT_VERIFIED (AGENTS.md "Merge Queue").
func RunReviewReportMerged(ctx core.Context, store config.CloudReadStore, alias, reviewID, buildID, remoteURL string, deps cloud.CloudDependencies) (model.PlatformReview, error) {
	if strings.TrimSpace(reviewID) == "" || strings.TrimSpace(remoteURL) == "" {
		return model.PlatformReview{}, fmt.Errorf("review id and remote url are required")
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformReview{}, err
	}
	details := []string{"status=MERGED"}
	if strings.TrimSpace(buildID) != "" {
		details = append(details, "buildId="+buildID)
	}
	details = append(details, "remoteUrl="+remoteURL)
	platformapi.TracePlatformCall(ctx, provider, "PATCH", "/v1/reviews/"+reviewID+"/status", details...)
	if ctx.DryRun {
		return model.PlatformReview{}, nil
	}
	return client.UpdateReviewStatus(context.Background(), reviewID, platformclient.PlatformUpdateReviewStatusParams{
		Status:    "MERGED",
		BuildID:   buildID,
		RemoteURL: remoteURL,
	})
}

// RunReviewRequeue moves a review at MERGE back to the tail of its queue as
// READY, using the backend's missed-merge-window PATCH. It bypasses no gate, so
// it takes no reason.
func RunReviewRequeue(ctx core.Context, store config.CloudReadStore, alias, reviewID string, deps cloud.CloudDependencies) (model.PlatformReview, error) {
	if strings.TrimSpace(reviewID) == "" {
		return model.PlatformReview{}, fmt.Errorf("review id is required")
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformReview{}, err
	}
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID)
	platformapi.TracePlatformCall(ctx, provider, "PATCH", "/v1/reviews/"+reviewID+"/status", "status=READY")
	if ctx.DryRun {
		return model.PlatformReview{}, nil
	}
	review, err := client.GetReview(context.Background(), reviewID)
	if err != nil {
		return model.PlatformReview{}, err
	}
	if review.Status != "MERGE" {
		return model.PlatformReview{}, fmt.Errorf("review %s is %s, not MERGE; requeue only recovers a review stuck at MERGE", reviewID, review.Status)
	}
	return client.UpdateReviewStatus(context.Background(), reviewID, platformclient.PlatformUpdateReviewStatusParams{Status: "READY"})
}

// RunReviewReviewersList lists the users assigned to review a review.
func RunReviewReviewersList(ctx core.Context, store config.CloudReadStore, alias, reviewID string, deps cloud.CloudDependencies) ([]model.PlatformReviewer, error) {
	if strings.TrimSpace(reviewID) == "" {
		return nil, fmt.Errorf("review id is required")
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID+"/reviewers")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListReviewers(context.Background(), reviewID)
}

// ensureReviewerIsEnrolledInCallerTenant refuses a cross-tenant userID before
// the network call; RLS scopes ListUsers to the caller's tenant.
func ensureReviewerIsEnrolledInCallerTenant(client *platformclient.PlatformClient, userID string) error {
	users, err := client.ListUsers(context.Background(), model.PlatformListUsersParams{})
	if err != nil {
		return fmt.Errorf("resolve your tenant's enrolled users: %w", err)
	}
	for _, user := range users {
		if user.UserID == userID {
			return nil
		}
	}
	return fmt.Errorf("user %s is not enrolled in your tenant; a reviewer must already be enrolled — see `erun platform user list`, or `erun platform user enroll` to add one", userID)
}

// RunReviewReviewerAdd assigns userID, who must be enrolled in the caller's tenant.
func RunReviewReviewerAdd(ctx core.Context, store config.CloudReadStore, alias, reviewID, userID string, deps cloud.CloudDependencies) (model.PlatformReviewer, error) {
	if strings.TrimSpace(reviewID) == "" || strings.TrimSpace(userID) == "" {
		return model.PlatformReviewer{}, fmt.Errorf("review id and user id are required")
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformReviewer{}, err
	}
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/users")
	platformapi.TracePlatformCall(ctx, provider, "POST", "/v1/reviews/"+reviewID+"/reviewers", "userId="+userID)
	if ctx.DryRun {
		return model.PlatformReviewer{}, nil
	}
	if err := ensureReviewerIsEnrolledInCallerTenant(client, userID); err != nil {
		return model.PlatformReviewer{}, err
	}
	return client.AddReviewer(context.Background(), reviewID, platformclient.PlatformAddReviewerParams{UserID: userID})
}

// RunReviewReviewerRemove unassigns userID from reviewID's reviewers.
func RunReviewReviewerRemove(ctx core.Context, store config.CloudReadStore, alias, reviewID, userID string, deps cloud.CloudDependencies) error {
	if strings.TrimSpace(reviewID) == "" || strings.TrimSpace(userID) == "" {
		return fmt.Errorf("review id and user id are required")
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return err
	}
	platformapi.TracePlatformCall(ctx, provider, "DELETE", "/v1/reviews/"+reviewID+"/reviewers/"+userID)
	if ctx.DryRun {
		return nil
	}
	return client.RemoveReviewer(context.Background(), reviewID, userID)
}

func checkoutOriginRemoteURL() (string, bool) {
	origin, err := OriginRemoteURL()
	if err != nil {
		return "", false
	}
	return origin, true
}

// resolveMergeQueueRepository resolves the queue's repository from the named
// remote or the checkout; empty addresses every repository's queue.
func resolveMergeQueueRepository(ctx core.Context, params model.PlatformMergeQueueParams, traceLabel string) (model.PlatformMergeQueueParams, error) {
	remote := strings.TrimSpace(params.Repository)
	if remote == "" {
		ctx.Trace(traceLabel + ": no repository given; reading origin from the current checkout")
		origin, ok := checkoutOriginRemoteURL()
		if !ok {
			// Not fatal: the platform refuses a promotion it cannot place.
			ctx.Trace(traceLabel + ": this checkout has no origin remote; the queue every repository shares is the one addressed")
			return params, nil
		}
		remote = origin
	}
	repository, err := core.RepositoryIdentity(remote)
	if err != nil {
		return model.PlatformMergeQueueParams{}, err
	}
	params.Repository = repository
	ctx.Trace(traceLabel + ": repository = " + repository)
	return params, nil
}

// RunReviewMergeQueueList lists one repository's merge queue for a target branch.
func RunReviewMergeQueueList(ctx core.Context, store config.CloudReadStore, alias string, params model.PlatformMergeQueueParams, deps cloud.CloudDependencies) ([]model.PlatformReview, error) {
	params, err := resolveMergeQueueRepository(ctx, params, "review queue list")
	if err != nil {
		return nil, err
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	platformapi.TracePlatformCall(ctx, provider, "GET", "/v1/reviews/merge-queue", mergeQueueTraceDetails(params)...)
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListMergeQueue(context.Background(), params)
}

// RunReviewMergeQueueAdvance promotes the queue head to MERGE, refusing with
// *PlatformMergeQueueBlockedError while it has unresolved threads.
func RunReviewMergeQueueAdvance(ctx core.Context, store config.CloudReadStore, alias string, params model.PlatformMergeQueueParams, deps cloud.CloudDependencies) (model.PlatformReview, error) {
	params, err := resolveMergeQueueRepository(ctx, params, "review queue advance")
	if err != nil {
		return model.PlatformReview{}, err
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformReview{}, err
	}
	platformapi.TracePlatformCall(ctx, provider, "POST", "/v1/reviews/merge-queue/advance", mergeQueueTraceDetails(params)...)
	if ctx.DryRun {
		return model.PlatformReview{}, nil
	}
	return client.AdvanceMergeQueue(context.Background(), params)
}

// RunReviewMergeQueueOverrideAdvance bypasses the unresolved-thread gate with a
// required, audited reason.
func RunReviewMergeQueueOverrideAdvance(ctx core.Context, store config.CloudReadStore, alias string, params model.PlatformMergeQueueParams, reason string, deps cloud.CloudDependencies) (model.PlatformReview, error) {
	if strings.TrimSpace(reason) == "" {
		return model.PlatformReview{}, fmt.Errorf("a reason is required to override the merge queue's unresolved-thread gate")
	}
	params, err := resolveMergeQueueRepository(ctx, params, "review queue override-advance")
	if err != nil {
		return model.PlatformReview{}, err
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return model.PlatformReview{}, err
	}
	details := append(mergeQueueTraceDetails(params), "reason="+reason)
	platformapi.TracePlatformCall(ctx, provider, "POST", "/v1/reviews/merge-queue/override-advance", details...)
	if ctx.DryRun {
		return model.PlatformReview{}, nil
	}
	return client.OverrideAdvanceMergeQueue(context.Background(), params, reason)
}

func mergeQueueTraceDetails(params model.PlatformMergeQueueParams) []string {
	var details []string
	if strings.TrimSpace(params.Repository) != "" {
		details = append(details, "repository="+params.Repository)
	}
	details = append(details, "targetBranch="+params.TargetBranch)
	return details
}
