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
	ReviewID     string `json:"reviewId"`
	TenantID     string `json:"tenantId"`
	AuthorUserID string `json:"authorUserId,omitempty"`
	// Repository is the repository the review's branches belong to, as a
	// canonical remote identity (RepositoryIdentity). Empty for a review
	// created before the platform recorded one: a tenant may serve more than
	// one repository, and without it a source/target branch pair names the
	// repository only by convention.
	Repository        string    `json:"repository,omitempty"`
	Name              string    `json:"name"`
	TargetBranch      string    `json:"targetBranch"`
	SourceBranch      string    `json:"sourceBranch"`
	Status            string    `json:"status"`
	LastFailedBuildID string    `json:"lastFailedBuildId,omitempty"`
	LastReadyBuildID  string    `json:"lastReadyBuildId,omitempty"`
	LastMergedBuildID string    `json:"lastMergedBuildId,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	// IssueRef and IssueRefSource mirror the review's derived issue link: the
	// issue the review's work belongs to, and whether that link was declared
	// or inferred from the source branch. They are resolved for the response
	// and never stored, and they are absent together when the review is linked
	// to no issue at all.
	IssueRef       string               `json:"issueRef,omitempty"`
	IssueRefSource IssueReferenceSource `json:"issueRefSource,omitempty"`
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
	BuildID  string `json:"buildId"`
	TenantID string `json:"tenantId"`
	// ReviewID is empty for a build with no review attached (erun#1954).
	ReviewID   string `json:"reviewId,omitempty"`
	ReviewName string `json:"reviewName,omitempty"`
	// EnvironmentID is the environment the build ran in, when the caller
	// reported one; empty for a review-linked build.
	EnvironmentID   string `json:"environmentId,omitempty"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Successful      bool   `json:"successful"`
	CommitID        string `json:"commitId"`
	Version         string `json:"version"`
	// FailureDetail is the caller's own account of why a RECORDED build
	// failed; empty for a successful build.
	FailureDetail string `json:"failureDetail,omitempty"`
	// Profile is the bounded per-step profile the caller collected for this
	// build, when it collected one.
	Profile   *BuildProfileSummary `json:"profile,omitempty"`
	CreatedAt time.Time            `json:"createdAt"`
	UpdatedAt time.Time            `json:"updatedAt"`
}

// PlatformReviewFilter mirrors the discovery filters GET /v1/reviews accepts.
// AuthorUserID selects "my reviews"; ReviewerUserID selects "reviews waiting
// on me" (a review whose review_reviewers includes that user); Repository
// narrows to one repository's reviews, which is what distinguishes two
// repositories a tenant serves that propose the same branch pair.
type PlatformReviewFilter struct {
	Repository     string
	TargetBranch   string
	SourceBranch   string
	Status         string
	AuthorUserID   string
	ReviewerUserID string
}

func (f PlatformReviewFilter) queryString() string {
	values := url.Values{}
	if strings.TrimSpace(f.Repository) != "" {
		values.Set("repository", f.Repository)
	}
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
// caller-settable: the backend always creates a review OPEN. Repository is
// the repository the branches belong to (RepositoryIdentity); the backend
// canonicalizes it, so any spelling of one repository is one repository here.
type PlatformCreateReviewParams struct {
	Repository   string `json:"repository,omitempty"`
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	// IssueRef is the issue this work belongs to, recorded on the review:
	// the canonical owner/repo#number, or the bare number a branch name
	// carries, which the platform joins to the review's own repository.
	// Recorded as declared, so it outranks whatever the source branch looks
	// like. A value that is neither shape is refused rather than stored.
	IssueRef string `json:"issueRef,omitempty"`
}

// CreateReview opens a review. name is the eventual squash-merge message and
// is unique per tenant and repository; a colliding name is reported as
// ErrPlatformConflict.
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
// fetches to verify the merge against. Which check it runs depends on where
// the review is — a review at MERGE is checked through BuildID's GATE build,
// and any other review through whether its source branch's changes are
// already in the target branch's history, which is why BuildID is optional.
// It must name the review's own repository; a remote for a different one is
// refused rather than verified, and a review that recorded no repository
// adopts the one this names.
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

// PlatformMergeQueueParams addresses one repository's merge queue. An empty
// TargetBranch lists every target branch's queue; an empty Repository lists
// every repository's, which is only meaningful for a tenant serving one.
type PlatformMergeQueueParams struct {
	Repository   string
	TargetBranch string
}

func (p PlatformMergeQueueParams) queryString() string {
	values := url.Values{}
	if strings.TrimSpace(p.TargetBranch) != "" {
		values.Set("targetBranch", p.TargetBranch)
	}
	if strings.TrimSpace(p.Repository) != "" {
		values.Set("repository", p.Repository)
	}
	return values.Encode()
}

// ListMergeQueue lists the reviews queued (or already READY) to merge into
// params' target branch, in queue order.
func (c *PlatformClient) ListMergeQueue(ctx context.Context, params PlatformMergeQueueParams) ([]PlatformReview, error) {
	path := "/v1/reviews/merge-queue"
	if query := params.queryString(); query != "" {
		path += "?" + query
	}
	var reviews []PlatformReview
	err := c.do(ctx, http.MethodGet, path, nil, true, &reviews)
	return reviews, err
}

// AdvanceMergeQueue advances params' merge queue head to MERGE, refusing with
// a *PlatformMergeQueueBlockedError (wrapping ErrPlatformConflict) when that
// review still has unresolved comment threads, and with a
// *PlatformMergeQueueOccupiedError when another review already holds that
// branch's single MERGE slot. OverrideAdvanceMergeQueue is the one deliberate,
// audited way past the thread refusal.
func (c *PlatformClient) AdvanceMergeQueue(ctx context.Context, params PlatformMergeQueueParams) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPost, "/v1/reviews/merge-queue/advance", map[string]string{"repository": params.Repository, "targetBranch": params.TargetBranch}, true, &review)
	return review, decorateMergeQueueRefusalError(err)
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

// PlatformMergeQueueOccupiedError decorates ErrPlatformConflict with the
// review already holding targetBranch's single MERGE slot. Without it a
// caller relayed the platform's own refusal as an opaque body; with it the
// operator is told which review to finish or requeue instead of being sent
// after a resource that was never missing.
type PlatformMergeQueueOccupiedError struct {
	TargetBranch string
	ReviewID     string
	Name         string
	SourceBranch string
	status       *PlatformStatusError
}

func (e *PlatformMergeQueueOccupiedError) Error() string {
	return fmt.Sprintf("merge queue for %s already has a review at MERGE: %s (%s, %s); complete it or requeue it back to READY before advancing",
		e.TargetBranch, e.ReviewID, e.Name, e.SourceBranch)
}

func (e *PlatformMergeQueueOccupiedError) Unwrap() error {
	return e.status
}

// mergeQueueOccupiedBody mirrors erun-backend-api's MERGE_QUEUE_OCCUPIED
// response — the standard {code, message, details} envelope, so the review
// fields are nested under details rather than flat the way
// unresolvedThreadsBody's bespoke shape carries them.
type mergeQueueOccupiedBody struct {
	Code    string `json:"code"`
	Details struct {
		TargetBranch string `json:"targetBranch"`
		ReviewID     string `json:"reviewId"`
		Name         string `json:"name"`
		SourceBranch string `json:"sourceBranch"`
	} `json:"details"`
}

// decorateMergeQueueRefusalError recognizes the structured refusals
// AdvanceMergeQueue's 409 can carry — an unresolved thread on the queue head,
// or another review holding the branch's MERGE slot — and wraps each as its
// own typed error; every other error (including a plain ErrPlatformConflict,
// and no error at all) passes through unchanged.
func decorateMergeQueueRefusalError(err error) error {
	var statusErr *PlatformStatusError
	if !errors.As(err, &statusErr) || statusErr.Status != http.StatusConflict {
		return err
	}
	var threads unresolvedThreadsBody
	if jsonErr := json.Unmarshal(statusErr.Body, &threads); jsonErr == nil && threads.Error == "unresolved_threads" {
		return &PlatformMergeQueueBlockedError{ReviewID: threads.ReviewID, UnresolvedThreads: threads.UnresolvedThreads, status: statusErr}
	}
	var occupied mergeQueueOccupiedBody
	if jsonErr := json.Unmarshal(statusErr.Body, &occupied); jsonErr == nil && occupied.Code == "MERGE_QUEUE_OCCUPIED" {
		return &PlatformMergeQueueOccupiedError{
			TargetBranch: occupied.Details.TargetBranch,
			ReviewID:     occupied.Details.ReviewID,
			Name:         occupied.Details.Name,
			SourceBranch: occupied.Details.SourceBranch,
			status:       statusErr,
		}
	}
	return err
}

// OverrideAdvanceMergeQueue bypasses AdvanceMergeQueue's unresolved-thread
// gate. reason is required — the backend refuses a blank one — and is
// recorded in the platform's audit trail alongside the caller's identity.
func (c *PlatformClient) OverrideAdvanceMergeQueue(ctx context.Context, params PlatformMergeQueueParams, reason string) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPost, "/v1/reviews/merge-queue/override-advance", map[string]string{"repository": params.Repository, "targetBranch": params.TargetBranch, "reason": reason}, true, &review)
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
	if strings.TrimSpace(filter.Repository) != "" {
		details = append(details, "repository="+filter.Repository)
	}
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
