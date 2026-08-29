package eruncommon

import (
	"context"
	"fmt"
	"strconv"
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
// UnresolvedThreads counts root comments (threads) still OPEN; a thread's
// status is its root comment's Status, since replies never carry their own.
type ReviewDetail struct {
	Review            PlatformReview    `json:"review"`
	Comments          []PlatformComment `json:"comments"`
	Builds            []PlatformBuild   `json:"builds"`
	UnresolvedThreads int               `json:"unresolvedThreads"`
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
	return ReviewDetail{Review: review, Comments: comments, Builds: builds, UnresolvedThreads: CountUnresolvedThreads(comments)}, nil
}

// CountUnresolvedThreads counts root comments (parentCommentId unset) whose
// status is still OPEN. A thread's status lives entirely on its root; replies
// never carry their own (comments_validate in erun-backend-db enforces this).
// Exported so other transports (erun-ui's tenant dashboard) can compute the
// same count from a comment list without re-deriving the rule.
func CountUnresolvedThreads(comments []PlatformComment) int {
	count := 0
	for _, comment := range comments {
		if strings.TrimSpace(comment.ParentCommentID) == "" && comment.Status == "OPEN" {
			count++
		}
	}
	return count
}

// threadRootCommentID reports commentID's thread root when commentID is a
// reply (parentCommentId set), so a resolve/unresolve call addressed to a
// reply can be refused with the root id the caller should retry against
// instead. A commentID absent from comments (e.g. a wrong review id) is
// reported as not-a-reply and left to the backend's own not-found error.
func threadRootCommentID(comments []PlatformComment, commentID string) (string, bool) {
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

// runReviewCommentStatus is the shared implementation behind RunReviewResolve
// and RunReviewUnresolve: it lists the review's comments (both to trace the
// lookup and to enforce that only a thread's root, never a reply, is
// addressable) before issuing the status PATCH.
func runReviewCommentStatus(ctx Context, store CloudReadStore, alias, reviewID, commentID, status string, deps CloudDependencies) (PlatformComment, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformComment{}, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID+"/comments")
	tracePlatformCall(ctx, provider, "PATCH", "/v1/reviews/"+reviewID+"/comments/"+commentID+"/status", "status="+status)
	if ctx.DryRun {
		return PlatformComment{}, nil
	}
	comments, err := client.ListComments(context.Background(), reviewID)
	if err != nil {
		return PlatformComment{}, err
	}
	if rootID, isReply := threadRootCommentID(comments, commentID); isReply {
		return PlatformComment{}, fmt.Errorf("comment %s is a reply; only a thread's root comment can be resolved or unresolved — retry against root comment %s", commentID, rootID)
	}
	return client.UpdateCommentStatus(context.Background(), reviewID, commentID, PlatformUpdateCommentStatusParams{Status: status})
}

// RunReviewResolve marks a comment thread CLOSED (resolved). commentID must
// be a thread's root comment; addressing a reply is refused.
func RunReviewResolve(ctx Context, store CloudReadStore, alias, reviewID, commentID string, deps CloudDependencies) (PlatformComment, error) {
	return runReviewCommentStatus(ctx, store, alias, reviewID, commentID, "CLOSED", deps)
}

// RunReviewUnresolve reopens a comment thread (marks it OPEN). commentID must
// be a thread's root comment; addressing a reply is refused.
func RunReviewUnresolve(ctx Context, store CloudReadStore, alias, reviewID, commentID string, deps CloudDependencies) (PlatformComment, error) {
	return runReviewCommentStatus(ctx, store, alias, reviewID, commentID, "OPEN", deps)
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

// ReviewRecordBuildParams is the `erun review record-build` input. Gate
// reports the merge queue's own GATE build kind instead of an ordinary
// RECORDED build — see AGENTS.md "Merge Queue". A GATE build carries no
// version, since the gate publishes nothing.
type ReviewRecordBuildParams struct {
	ReviewID      string
	CommitID      string
	Gate          bool
	Version       string
	Successful    bool
	FailureDetail string
}

// RunReviewRecordBuild records a build against a review. This is the
// primitive that actually moves a review off OPEN: the backend transitions it
// to READY (successful) or FAILED (not) as part of recording the build, and
// promotes it to MERGE if it was already the merge queue's head — there is no
// separate "set review status" call for this, by design (a READY with no
// build is a different thing: the missed-merge-window requeue).
func RunReviewRecordBuild(ctx Context, store CloudReadStore, alias string, params ReviewRecordBuildParams, deps CloudDependencies) (PlatformBuild, error) {
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformBuild{}, err
	}
	kind := ""
	if params.Gate {
		kind = "GATE"
	}
	details := []string{
		"commitId=" + params.CommitID,
	}
	if kind != "" {
		details = append(details, "kind="+kind)
	}
	details = append(details,
		"version="+params.Version,
		"successful="+strconv.FormatBool(params.Successful),
	)
	if strings.TrimSpace(params.FailureDetail) != "" {
		details = append(details, "failureDetail="+params.FailureDetail)
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/reviews/"+params.ReviewID+"/builds", details...)
	if ctx.DryRun {
		return PlatformBuild{}, nil
	}
	return client.CreateBuild(context.Background(), params.ReviewID, PlatformCreateBuildParams{
		CommitID:      params.CommitID,
		Kind:          kind,
		Version:       params.Version,
		Successful:    params.Successful,
		FailureDetail: params.FailureDetail,
	})
}

// RunReviewReportMerged reports a review MERGED after its promoted
// environment has fetched, gate-built, and pushed the prospective merge
// itself. buildId must name the GATE build the push actually produced;
// remoteURL is the git remote the platform fetches to verify that build's
// commit is really reachable from the target branch's tip. Any of the
// platform's three verification conditions failing refuses with
// MERGE_NOT_VERIFIED and leaves the review at MERGE — see AGENTS.md "Merge
// Queue".
func RunReviewReportMerged(ctx Context, store CloudReadStore, alias, reviewID, buildID, remoteURL string, deps CloudDependencies) (PlatformReview, error) {
	if strings.TrimSpace(reviewID) == "" || strings.TrimSpace(buildID) == "" || strings.TrimSpace(remoteURL) == "" {
		return PlatformReview{}, fmt.Errorf("review id, build id, and remote url are required")
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformReview{}, err
	}
	tracePlatformCall(ctx, provider, "PATCH", "/v1/reviews/"+reviewID+"/status", "status=MERGED", "buildId="+buildID, "remoteUrl="+remoteURL)
	if ctx.DryRun {
		return PlatformReview{}, nil
	}
	return client.UpdateReviewStatus(context.Background(), reviewID, PlatformUpdateReviewStatusParams{
		Status:    "MERGED",
		BuildID:   buildID,
		RemoteURL: remoteURL,
	})
}

// RunReviewReviewersList lists the users assigned to review a review.
func RunReviewReviewersList(ctx Context, store CloudReadStore, alias, reviewID string, deps CloudDependencies) ([]PlatformReviewer, error) {
	if strings.TrimSpace(reviewID) == "" {
		return nil, fmt.Errorf("review id is required")
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return nil, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/reviews/"+reviewID+"/reviewers")
	if ctx.DryRun {
		return nil, nil
	}
	return client.ListReviewers(context.Background(), reviewID)
}

// ensureReviewerIsEnrolledInCallerTenant refuses a cross-tenant userId before
// the network call that would assign it, rather than leaving that refusal to
// the backend's tenant-scoped foreign key alone (collaboration/reviews): it
// resolves the caller's own tenant's enrolled users (RLS scopes ListUsers's
// empty TenantID to the caller's tenant) and checks userID is one of them.
func ensureReviewerIsEnrolledInCallerTenant(client *PlatformClient, userID string) error {
	users, err := client.ListUsers(context.Background(), PlatformListUsersParams{})
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

// RunReviewReviewerAdd assigns userID as a reviewer on reviewID. A userID not
// enrolled in the caller's own tenant is refused before the network call.
func RunReviewReviewerAdd(ctx Context, store CloudReadStore, alias, reviewID, userID string, deps CloudDependencies) (PlatformReviewer, error) {
	if strings.TrimSpace(reviewID) == "" || strings.TrimSpace(userID) == "" {
		return PlatformReviewer{}, fmt.Errorf("review id and user id are required")
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformReviewer{}, err
	}
	tracePlatformCall(ctx, provider, "GET", "/v1/users")
	tracePlatformCall(ctx, provider, "POST", "/v1/reviews/"+reviewID+"/reviewers", "userId="+userID)
	if ctx.DryRun {
		return PlatformReviewer{}, nil
	}
	if err := ensureReviewerIsEnrolledInCallerTenant(client, userID); err != nil {
		return PlatformReviewer{}, err
	}
	return client.AddReviewer(context.Background(), reviewID, PlatformAddReviewerParams{UserID: userID})
}

// RunReviewReviewerRemove unassigns userID from reviewID's reviewers.
func RunReviewReviewerRemove(ctx Context, store CloudReadStore, alias, reviewID, userID string, deps CloudDependencies) error {
	if strings.TrimSpace(reviewID) == "" || strings.TrimSpace(userID) == "" {
		return fmt.Errorf("review id and user id are required")
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return err
	}
	tracePlatformCall(ctx, provider, "DELETE", "/v1/reviews/"+reviewID+"/reviewers/"+userID)
	if ctx.DryRun {
		return nil
	}
	return client.RemoveReviewer(context.Background(), reviewID, userID)
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
// MERGE, refusing with a *PlatformMergeQueueBlockedError when that review
// still has unresolved comment threads. RunReviewMergeQueueOverrideAdvance is
// the deliberate, audited way past that refusal.
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

// RunReviewMergeQueueOverrideAdvance bypasses RunReviewMergeQueueAdvance's
// unresolved-thread gate. reason is required and is recorded in the
// platform's audit trail; a blank one is refused before the network call.
func RunReviewMergeQueueOverrideAdvance(ctx Context, store CloudReadStore, alias, targetBranch, reason string, deps CloudDependencies) (PlatformReview, error) {
	if strings.TrimSpace(reason) == "" {
		return PlatformReview{}, fmt.Errorf("a reason is required to override the merge queue's unresolved-thread gate")
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, alias, deps)
	if err != nil {
		return PlatformReview{}, err
	}
	tracePlatformCall(ctx, provider, "POST", "/v1/reviews/merge-queue/override-advance", "targetBranch="+targetBranch, "reason="+reason)
	if ctx.DryRun {
		return PlatformReview{}, nil
	}
	return client.OverrideAdvanceMergeQueue(context.Background(), targetBranch, reason)
}
