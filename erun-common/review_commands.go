package eruncommon

import (
	"context"
	"fmt"
	"strings"
)

// review_commands.go is the shared planning/execution layer `erun review`
// (CLI) and its MCP tools both drive, mirroring platform_commands.go: resolve
// the erun platform alias, build a PlatformClient, trace the resolved HTTP
// call so --dry-run (CLI) and a preview path (MCP) never touch the network,
// then perform it for real.

// ReviewListParams is the `erun review list` input. Mine and WaitingOnMe are
// convenience filters that resolve to the caller's own user id via a whoami
// call rather than requiring the caller to already know it.
type ReviewListParams struct {
	TargetBranch   string
	SourceBranch   string
	Status         string
	AuthorUserID   string
	ReviewerUserID string
	Mine           bool
	WaitingOnMe    bool
}

// RunReviewList lists reviews visible to the caller's tenant, narrowed by the
// given filters.
func RunReviewList(ctx Context, store CloudReadStore, alias string, params ReviewListParams, deps CloudDependencies) ([]PlatformReview, error) {
	if err := validateReviewListParams(params); err != nil {
		return nil, err
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
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

func validateReviewListParams(params ReviewListParams) error {
	if params.Mine && strings.TrimSpace(params.AuthorUserID) != "" {
		return fmt.Errorf("cannot combine --mine with an explicit author user id")
	}
	if params.WaitingOnMe && strings.TrimSpace(params.ReviewerUserID) != "" {
		return fmt.Errorf("cannot combine --waiting-on-me with an explicit reviewer user id")
	}
	return nil
}

func reviewListFilter(params ReviewListParams) PlatformReviewFilter {
	return PlatformReviewFilter{
		TargetBranch:   params.TargetBranch,
		SourceBranch:   params.SourceBranch,
		Status:         params.Status,
		AuthorUserID:   params.AuthorUserID,
		ReviewerUserID: params.ReviewerUserID,
	}
}

// traceReviewList traces every network call RunReviewList would make in
// order, including the whoami lookup --mine/--waiting-on-me trigger, so a
// dry run's trace matches what a real run actually does.
func traceReviewList(ctx Context, provider CloudProviderConfig, filter PlatformReviewFilter, params ReviewListParams) {
	details := reviewFilterTraceDetails(filter)
	if params.Mine {
		details = append(details, "authorUserId=<caller>")
	}
	if params.WaitingOnMe {
		details = append(details, "reviewerUserId=<caller>")
	}
	if params.Mine || params.WaitingOnMe {
		tracePlatformCall(ctx, provider, "GET", "/v1/whoami")
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/reviews", details...)
}

// resolveReviewListCallerFilters resolves --mine/--waiting-on-me to the
// caller's own user id via whoami, filling filter in place.
func resolveReviewListCallerFilters(client *PlatformClient, params ReviewListParams, filter *PlatformReviewFilter) error {
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

// ReviewDetail is the combined result of `erun review show`: the review
// itself alongside its comment threads and recorded builds, so a caller sees
// everything needed to read and act on a review from one command.
type ReviewDetail struct {
	Review   PlatformReview    `json:"review"`
	Comments []PlatformComment `json:"comments"`
	Builds   []PlatformBuild   `json:"builds"`
}

// RunReviewShow fetches one review together with its comments and builds.
func RunReviewShow(ctx Context, store CloudReadStore, alias, reviewID string, deps CloudDependencies) (ReviewDetail, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return ReviewDetail{}, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID)
	tracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID+"/comments")
	tracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID+"/builds")
	if ctx.DryRun {
		return ReviewDetail{}, nil
	}
	review, err := client.GetReview(context.Background(), reviewID)
	if err != nil {
		return ReviewDetail{}, err
	}
	comments, err := client.ListComments(context.Background(), reviewID)
	if err != nil {
		return ReviewDetail{}, err
	}
	builds, err := client.ListBuilds(context.Background(), reviewID)
	if err != nil {
		return ReviewDetail{}, err
	}
	return ReviewDetail{Review: review, Comments: comments, Builds: builds}, nil
}

// RunReviewCreate opens a review.
func RunReviewCreate(ctx Context, store CloudReadStore, alias string, params PlatformCreateReviewParams, deps CloudDependencies) (PlatformReview, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformReview{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/reviews",
		"name="+params.Name, "targetBranch="+params.TargetBranch, "sourceBranch="+params.SourceBranch)
	if ctx.DryRun {
		return PlatformReview{}, nil
	}
	return client.CreateReview(context.Background(), params)
}

// ReviewCommentParams is the `erun review comment` input. ParentCommentID,
// when set, makes this a reply in an existing thread.
type ReviewCommentParams struct {
	ReviewID        string
	CommitID        string
	FilePath        string
	Line            int
	Body            string
	ParentCommentID string
}

// RunReviewComment posts a comment (or a reply) on a review.
func RunReviewComment(ctx Context, store CloudReadStore, alias string, params ReviewCommentParams, deps CloudDependencies) (PlatformComment, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformComment{}, err
	}
	details := []string{
		"commitId=" + params.CommitID,
		"filePath=" + params.FilePath,
		"line=" + formatCommentLine(params.Line),
	}
	if strings.TrimSpace(params.ParentCommentID) != "" {
		details = append(details, "replyTo="+params.ParentCommentID)
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/reviews/"+params.ReviewID+"/comments", details...)
	if ctx.DryRun {
		return PlatformComment{}, nil
	}
	return client.CreateComment(context.Background(), params.ReviewID, PlatformCreateCommentParams{
		CommitID:        params.CommitID,
		FilePath:        params.FilePath,
		Line:            params.Line,
		Body:            params.Body,
		ParentCommentID: params.ParentCommentID,
	})
}

// RunReviewClose closes a review.
func RunReviewClose(ctx Context, store CloudReadStore, alias, reviewID string, deps CloudDependencies) (PlatformReview, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformReview{}, err
	}
	tracePlatformCall(ctx, provider, "PATCH", "/v1/reviews/"+reviewID+"/status", "status=CLOSED")
	if ctx.DryRun {
		return PlatformReview{}, nil
	}
	return client.UpdateReviewStatus(context.Background(), reviewID, PlatformUpdateReviewStatusParams{Status: "CLOSED"})
}

// RunReviewMergeQueueList lists targetBranch's merge queue.
func RunReviewMergeQueueList(ctx Context, store CloudReadStore, alias, targetBranch string, deps CloudDependencies) ([]PlatformReview, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/reviews/merge-queue", "targetBranch="+targetBranch)
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListMergeQueue(context.Background(), targetBranch)
}

// RunReviewMergeQueueAdvance advances targetBranch's merge queue head to
// MERGED.
func RunReviewMergeQueueAdvance(ctx Context, store CloudReadStore, alias, targetBranch string, deps CloudDependencies) (PlatformReview, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformReview{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/reviews/merge-queue/advance", "targetBranch="+targetBranch)
	if ctx.DryRun {
		return PlatformReview{}, nil
	}
	return client.AdvanceMergeQueue(context.Background(), targetBranch)
}
