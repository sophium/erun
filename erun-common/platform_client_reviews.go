package eruncommon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// platform_client_reviews.go extends PlatformClient with the collaboration
// surface (reviews, comments, builds, merge queue) so erun-cli's `erun
// review` and erun-mcp's `review_*` tools share one client and one set of
// wire shapes, rather than each hand-rolling HTTP against erun-backend-api
// (#1199).

// PlatformReview mirrors model.Review's JSON shape.
type PlatformReview struct {
	ReviewID          string    `json:"reviewId"`
	TenantID          string    `json:"tenantId"`
	AuthorUserID      string    `json:"authorUserId,omitempty"`
	Name              string    `json:"name"`
	TargetBranch      string    `json:"targetBranch"`
	SourceBranch      string    `json:"sourceBranch"`
	Status            string    `json:"status"`
	LastFailedBuildID string    `json:"lastFailedBuildId,omitempty"`
	LastReadyBuildID  string    `json:"lastReadyBuildId,omitempty"`
	LastMergedBuildID string    `json:"lastMergedBuildId,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// PlatformComment mirrors model.Comment's JSON shape.
type PlatformComment struct {
	CommentID       string    `json:"commentId"`
	TenantID        string    `json:"tenantId"`
	ReviewID        string    `json:"reviewId"`
	CreatorUserID   string    `json:"creatorUserId,omitempty"`
	Status          string    `json:"status"`
	ParentCommentID string    `json:"parentCommentId,omitempty"`
	CommitID        string    `json:"commitId"`
	FilePath        string    `json:"filePath"`
	Line            int       `json:"line"`
	Body            string    `json:"body"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// PlatformReviewer mirrors model.ReviewReviewer's JSON shape.
type PlatformReviewer struct {
	ReviewID  string    `json:"reviewId"`
	UserID    string    `json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PlatformBuild mirrors model.Build's JSON shape.
type PlatformBuild struct {
	BuildID    string `json:"buildId"`
	TenantID   string `json:"tenantId"`
	ReviewID   string `json:"reviewId"`
	ReviewName string `json:"reviewName,omitempty"`
	Successful bool   `json:"successful"`
	CommitID   string `json:"commitId"`
	Version    string `json:"version"`
	// FailureDetail is the caller's own account of why a RECORDED build
	// failed; empty for a successful build.
	FailureDetail string    `json:"failureDetail,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// PlatformReviewFilter mirrors the discovery filters GET /v1/reviews accepts.
// AuthorUserID selects "my reviews"; ReviewerUserID selects "reviews waiting
// on me" (a review whose review_reviewers includes that user).
type PlatformReviewFilter struct {
	TargetBranch   string
	SourceBranch   string
	Status         string
	AuthorUserID   string
	ReviewerUserID string
}

func (f PlatformReviewFilter) queryString() string {
	values := url.Values{}
	if strings.TrimSpace(f.TargetBranch) != "" {
		values.Set("targetBranch", f.TargetBranch)
	}
	if strings.TrimSpace(f.SourceBranch) != "" {
		values.Set("sourceBranch", f.SourceBranch)
	}
	if strings.TrimSpace(f.Status) != "" {
		values.Set("status", f.Status)
	}
	if strings.TrimSpace(f.AuthorUserID) != "" {
		values.Set("authorUserId", f.AuthorUserID)
	}
	if strings.TrimSpace(f.ReviewerUserID) != "" {
		values.Set("reviewerUserId", f.ReviewerUserID)
	}
	return values.Encode()
}

// ListReviews lists reviews visible to the caller's tenant, narrowed by
// filter.
func (c *PlatformClient) ListReviews(ctx context.Context, filter PlatformReviewFilter) ([]PlatformReview, error) {
	path := "/v1/reviews"
	if query := filter.queryString(); query != "" {
		path += "?" + query
	}
	var reviews []PlatformReview
	err := c.do(ctx, http.MethodGet, path, nil, true, &reviews)
	return reviews, err
}

// PlatformCreateReviewParams is the review-creation input. Status is not
// caller-settable: the backend always creates a review OPEN.
type PlatformCreateReviewParams struct {
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
}

// CreateReview opens a review. name is the eventual squash-merge message and
// is unique per tenant; a colliding name is reported as ErrPlatformConflict.
func (c *PlatformClient) CreateReview(ctx context.Context, params PlatformCreateReviewParams) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPost, "/v1/reviews", params, true, &review)
	return review, err
}

// GetReview fetches one review by id.
func (c *PlatformClient) GetReview(ctx context.Context, reviewID string) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodGet, "/v1/reviews/"+url.PathEscape(reviewID), nil, true, &review)
	return review, err
}

// PlatformUpdateReviewStatusParams is the review status-transition input.
// RemoteURL is required only for a MERGED report: the git remote the platform
// fetches to verify buildId's commit is really reachable from the target
// branch's tip with the parent this review was gated against.
type PlatformUpdateReviewStatusParams struct {
	Status    string `json:"status"`
	BuildID   string `json:"buildId,omitempty"`
	RemoteURL string `json:"remoteUrl,omitempty"`
}

// UpdateReviewStatus transitions a review's status (e.g. to CLOSED).
func (c *PlatformClient) UpdateReviewStatus(ctx context.Context, reviewID string, params PlatformUpdateReviewStatusParams) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPatch, "/v1/reviews/"+url.PathEscape(reviewID)+"/status", params, true, &review)
	return review, err
}

// ListMergeQueue lists the reviews queued (or already READY) to merge into
// targetBranch, in queue order. An empty targetBranch lists every queued
// review, across target branches.
func (c *PlatformClient) ListMergeQueue(ctx context.Context, targetBranch string) ([]PlatformReview, error) {
	path := "/v1/reviews/merge-queue"
	if strings.TrimSpace(targetBranch) != "" {
		path += "?targetBranch=" + url.QueryEscape(targetBranch)
	}
	var reviews []PlatformReview
	err := c.do(ctx, http.MethodGet, path, nil, true, &reviews)
	return reviews, err
}

// AdvanceMergeQueue advances targetBranch's merge queue head to MERGE,
// refusing with a *PlatformMergeQueueBlockedError (wrapping ErrPlatformConflict)
// when that review still has unresolved comment threads.
// OverrideAdvanceMergeQueue is the one deliberate, audited way past that
// refusal.
func (c *PlatformClient) AdvanceMergeQueue(ctx context.Context, targetBranch string) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPost, "/v1/reviews/merge-queue/advance", map[string]string{"targetBranch": targetBranch}, true, &review)
	return review, decorateMergeQueueBlockedError(err)
}

// PlatformMergeQueueBlockedError decorates ErrPlatformConflict with the
// review and unresolved-thread count AdvanceMergeQueue refused on, so a
// caller can report both and route the operator to the review without
// re-parsing the response body itself.
type PlatformMergeQueueBlockedError struct {
	ReviewID          string
	UnresolvedThreads int
	status            *PlatformStatusError
}

func (e *PlatformMergeQueueBlockedError) Error() string {
	return fmt.Sprintf("review %s has %d unresolved comment thread(s); resolve them or use the merge queue override", e.ReviewID, e.UnresolvedThreads)
}

func (e *PlatformMergeQueueBlockedError) Unwrap() error {
	return e.status
}

// unresolvedThreadsBody mirrors routes.unresolvedThreadsResponse in
// erun-backend-api (the JSON body AdvanceMergeQueue's 409 carries).
type unresolvedThreadsBody struct {
	Error             string `json:"error"`
	ReviewID          string `json:"reviewId"`
	UnresolvedThreads int    `json:"unresolvedThreads"`
}

// decorateMergeQueueBlockedError recognizes AdvanceMergeQueue's structured
// unresolved-thread refusal and wraps it as a PlatformMergeQueueBlockedError;
// every other error (including a plain ErrPlatformConflict, and no error at
// all) passes through unchanged.
func decorateMergeQueueBlockedError(err error) error {
	var statusErr *PlatformStatusError
	if !errors.As(err, &statusErr) || statusErr.Status != http.StatusConflict {
		return err
	}
	var body unresolvedThreadsBody
	if jsonErr := json.Unmarshal(statusErr.Body, &body); jsonErr != nil || body.Error != "unresolved_threads" {
		return err
	}
	return &PlatformMergeQueueBlockedError{ReviewID: body.ReviewID, UnresolvedThreads: body.UnresolvedThreads, status: statusErr}
}

// OverrideAdvanceMergeQueue bypasses AdvanceMergeQueue's unresolved-thread
// gate. reason is required — the backend refuses a blank one — and is
// recorded in the platform's audit trail alongside the caller's identity.
func (c *PlatformClient) OverrideAdvanceMergeQueue(ctx context.Context, targetBranch, reason string) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPost, "/v1/reviews/merge-queue/override-advance", map[string]string{"targetBranch": targetBranch, "reason": reason}, true, &review)
	return review, err
}

// ListComments lists a review's comment threads.
func (c *PlatformClient) ListComments(ctx context.Context, reviewID string) ([]PlatformComment, error) {
	var comments []PlatformComment
	err := c.do(ctx, http.MethodGet, "/v1/reviews/"+url.PathEscape(reviewID)+"/comments", nil, true, &comments)
	return comments, err
}

// PlatformCreateCommentParams is the comment-creation input. ParentCommentID,
// when set, makes this a reply in an existing thread.
type PlatformCreateCommentParams struct {
	CommitID        string `json:"commitId"`
	FilePath        string `json:"filePath"`
	Line            int    `json:"line"`
	Body            string `json:"body"`
	ParentCommentID string `json:"parentCommentId,omitempty"`
}

// CreateComment posts a comment (or a reply, with ParentCommentID set) on a
// review.
func (c *PlatformClient) CreateComment(ctx context.Context, reviewID string, params PlatformCreateCommentParams) (PlatformComment, error) {
	var comment PlatformComment
	err := c.do(ctx, http.MethodPost, "/v1/reviews/"+url.PathEscape(reviewID)+"/comments", params, true, &comment)
	return comment, err
}

// PlatformUpdateCommentStatusParams is the comment status-transition input.
type PlatformUpdateCommentStatusParams struct {
	Status string `json:"status"`
}

// UpdateCommentStatus transitions a comment thread's status (OPEN/CLOSED).
// Only a thread's root comment carries a meaningful status; the backend
// refuses a status change addressed to a reply.
func (c *PlatformClient) UpdateCommentStatus(ctx context.Context, reviewID, commentID string, params PlatformUpdateCommentStatusParams) (PlatformComment, error) {
	var comment PlatformComment
	err := c.do(ctx, http.MethodPatch, "/v1/reviews/"+url.PathEscape(reviewID)+"/comments/"+url.PathEscape(commentID)+"/status", params, true, &comment)
	return comment, err
}

// ListReviewers lists the users assigned to review a review.
func (c *PlatformClient) ListReviewers(ctx context.Context, reviewID string) ([]PlatformReviewer, error) {
	var reviewers []PlatformReviewer
	err := c.do(ctx, http.MethodGet, "/v1/reviews/"+url.PathEscape(reviewID)+"/reviewers", nil, true, &reviewers)
	return reviewers, err
}

// PlatformAddReviewerParams is the reviewer-assignment input.
type PlatformAddReviewerParams struct {
	UserID string `json:"userId"`
}

// AddReviewer assigns userId as a reviewer on a review. The platform refuses
// (ErrPlatformConflict) an already-assigned userId, and refuses
// (ErrPlatformNotFound, via the tenant-scoped foreign key) a userId outside
// the caller's tenant.
func (c *PlatformClient) AddReviewer(ctx context.Context, reviewID string, params PlatformAddReviewerParams) (PlatformReviewer, error) {
	var reviewer PlatformReviewer
	err := c.do(ctx, http.MethodPost, "/v1/reviews/"+url.PathEscape(reviewID)+"/reviewers", params, true, &reviewer)
	return reviewer, err
}

// RemoveReviewer unassigns userId from a review's reviewers.
func (c *PlatformClient) RemoveReviewer(ctx context.Context, reviewID, userID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/reviews/"+url.PathEscape(reviewID)+"/reviewers/"+url.PathEscape(userID), nil, true, nil)
}

// ListBuilds lists the builds recorded against a review.
func (c *PlatformClient) ListBuilds(ctx context.Context, reviewID string) ([]PlatformBuild, error) {
	var builds []PlatformBuild
	err := c.do(ctx, http.MethodGet, "/v1/reviews/"+url.PathEscape(reviewID)+"/builds", nil, true, &builds)
	return builds, err
}

// PlatformCreateBuildParams is the build-recording input. Kind is empty for
// an ordinary client-reported build (the backend defaults it to RECORDED) or
// "GATE" for a merge-queue gate build the caller ran itself against the
// prospective merge — see AGENTS.md "Merge Queue". Only a successful GATE
// build's commit can later be reported MERGED.
type PlatformCreateBuildParams struct {
	CommitID      string `json:"commitId"`
	Kind          string `json:"kind,omitempty"`
	Version       string `json:"version"`
	Successful    bool   `json:"successful"`
	FailureDetail string `json:"failureDetail,omitempty"`
}

// CreateBuild records a build against a review. Recording one is the sole way
// an erun client advances a review off OPEN: the backend transitions the
// review to READY (successful) or FAILED (not) as part of the same write, and
// promotes it to MERGE if it was already the merge queue's head.
func (c *PlatformClient) CreateBuild(ctx context.Context, reviewID string, params PlatformCreateBuildParams) (PlatformBuild, error) {
	var build PlatformBuild
	err := c.do(ctx, http.MethodPost, "/v1/reviews/"+url.PathEscape(reviewID)+"/builds", params, true, &build)
	return build, err
}

// reviewStatusQueryDetail renders a filter's set fields as tracePlatformCall
// detail strings, in a fixed order so a dry-run trace is deterministic.
func reviewFilterTraceDetails(filter PlatformReviewFilter) []string {
	var details []string
	if strings.TrimSpace(filter.TargetBranch) != "" {
		details = append(details, "targetBranch="+filter.TargetBranch)
	}
	if strings.TrimSpace(filter.SourceBranch) != "" {
		details = append(details, "sourceBranch="+filter.SourceBranch)
	}
	if strings.TrimSpace(filter.Status) != "" {
		details = append(details, "status="+filter.Status)
	}
	if strings.TrimSpace(filter.AuthorUserID) != "" {
		details = append(details, "authorUserId="+filter.AuthorUserID)
	}
	if strings.TrimSpace(filter.ReviewerUserID) != "" {
		details = append(details, "reviewerUserId="+filter.ReviewerUserID)
	}
	return details
}

// formatCommentLine renders a comment's line for a trace detail.
func formatCommentLine(line int) string {
	return strconv.Itoa(line)
}
