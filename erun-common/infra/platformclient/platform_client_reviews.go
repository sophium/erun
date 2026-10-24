package platformclient

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

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// PlatformReview mirrors model.Review's JSON shape.
type PlatformReview struct {
	ReviewID     string `json:"reviewId"`
	TenantID     string `json:"tenantId"`
	AuthorUserID string `json:"authorUserId,omitempty"`
	// Repository is the canonical remote identity; empty on reviews predating it.
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
	// IssueRef and IssueRefSource are derived per response, never stored.
	IssueRef       string                     `json:"issueRef,omitempty"`
	IssueRefSource model.IssueReferenceSource `json:"issueRefSource,omitempty"`
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
	BuildID         string                    `json:"buildId"`
	TenantID        string                    `json:"tenantId"`
	ReviewID        string                    `json:"reviewId,omitempty"`
	ReviewName      string                    `json:"reviewName,omitempty"`
	EnvironmentID   string                    `json:"environmentId,omitempty"`
	EnvironmentName string                    `json:"environmentName,omitempty"`
	Successful      bool                      `json:"successful"`
	CommitID        string                    `json:"commitId"`
	Version         string                    `json:"version"`
	FailureDetail   string                    `json:"failureDetail,omitempty"`
	Profile         *core.BuildProfileSummary `json:"profile,omitempty"`
	CreatedAt       time.Time                 `json:"createdAt"`
	UpdatedAt       time.Time                 `json:"updatedAt"`
}

// PlatformReviewFilter mirrors GET /v1/reviews' filters. ReviewerUserID selects
// reviews waiting on that user.
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

// ListReviews lists the tenant's reviews narrowed by filter.
func (c *PlatformClient) ListReviews(ctx context.Context, filter PlatformReviewFilter) ([]PlatformReview, error) {
	path := "/v1/reviews"
	if query := filter.queryString(); query != "" {
		path += "?" + query
	}
	var reviews []PlatformReview
	err := c.do(ctx, http.MethodGet, path, nil, true, &reviews)
	return reviews, err
}

// PlatformCreateReviewParams creates an OPEN review; the backend canonicalizes Repository.
type PlatformCreateReviewParams struct {
	Repository   string `json:"repository,omitempty"`
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	// IssueRef is owner/repo#number or a bare number joined to the review's
	// repository; it outranks the branch-name inference.
	IssueRef string `json:"issueRef,omitempty"`
}

// CreateReview opens a review. name is the squash-merge message, unique per
// tenant and repository (ErrPlatformConflict otherwise).
func (c *PlatformClient) CreateReview(ctx context.Context, params PlatformCreateReviewParams) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPost, "/v1/reviews", params, true, &review)
	return review, err
}

func (c *PlatformClient) GetReview(ctx context.Context, reviewID string) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodGet, "/v1/reviews/"+url.PathEscape(reviewID), nil, true, &review)
	return review, err
}

// PlatformUpdateReviewStatusParams transitions a review. A MERGED report needs
// RemoteURL, which must name the review's repository; BuildID is required only
// for a review at MERGE, the others are verified against target history.
type PlatformUpdateReviewStatusParams struct {
	Status    string `json:"status"`
	BuildID   string `json:"buildId,omitempty"`
	RemoteURL string `json:"remoteUrl,omitempty"`
}

// UpdateReviewStatus transitions a review's status.
func (c *PlatformClient) UpdateReviewStatus(ctx context.Context, reviewID string, params PlatformUpdateReviewStatusParams) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPatch, "/v1/reviews/"+url.PathEscape(reviewID)+"/status", params, true, &review)
	return review, err
}

// PlatformMergeQueueParams addresses a merge queue; empty fields list all.
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

// ListMergeQueue lists the queued reviews in queue order.
func (c *PlatformClient) ListMergeQueue(ctx context.Context, params PlatformMergeQueueParams) ([]PlatformReview, error) {
	path := "/v1/reviews/merge-queue"
	if query := params.queryString(); query != "" {
		path += "?" + query
	}
	var reviews []PlatformReview
	err := c.do(ctx, http.MethodGet, path, nil, true, &reviews)
	return reviews, err
}

// AdvanceMergeQueue moves the queue head to MERGE. It returns
// PlatformMergeQueueBlockedError on unresolved threads and
// PlatformMergeQueueOccupiedError when the MERGE slot is taken.
func (c *PlatformClient) AdvanceMergeQueue(ctx context.Context, params PlatformMergeQueueParams) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPost, "/v1/reviews/merge-queue/advance", map[string]string{"repository": params.Repository, "targetBranch": params.TargetBranch}, true, &review)
	return review, decorateMergeQueueRefusalError(err)
}

// PlatformMergeQueueBlockedError reports the review and unresolved-thread count.
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

// unresolvedThreadsBody mirrors the backend's unresolved-threads 409 body.
type unresolvedThreadsBody struct {
	Error             string `json:"error"`
	ReviewID          string `json:"reviewId"`
	UnresolvedThreads int    `json:"unresolvedThreads"`
}

// PlatformMergeQueueOccupiedError names the review holding the branch's MERGE slot.
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

// mergeQueueOccupiedBody mirrors the MERGE_QUEUE_OCCUPIED {code, details} envelope.
type mergeQueueOccupiedBody struct {
	Code    string `json:"code"`
	Details struct {
		TargetBranch string `json:"targetBranch"`
		ReviewID     string `json:"reviewId"`
		Name         string `json:"name"`
		SourceBranch string `json:"sourceBranch"`
	} `json:"details"`
}

// decorateMergeQueueRefusalError wraps recognized 409 bodies as typed errors.
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

// OverrideAdvanceMergeQueue bypasses the unresolved-thread gate; reason is
// required and audited.
func (c *PlatformClient) OverrideAdvanceMergeQueue(ctx context.Context, params PlatformMergeQueueParams, reason string) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPost, "/v1/reviews/merge-queue/override-advance", map[string]string{"repository": params.Repository, "targetBranch": params.TargetBranch, "reason": reason}, true, &review)
	return review, err
}

func (c *PlatformClient) ListComments(ctx context.Context, reviewID string) ([]PlatformComment, error) {
	var comments []PlatformComment
	err := c.do(ctx, http.MethodGet, "/v1/reviews/"+url.PathEscape(reviewID)+"/comments", nil, true, &comments)
	return comments, err
}

// PlatformCreateCommentParams.ParentCommentID makes the comment a reply.
type PlatformCreateCommentParams struct {
	CommitID        string `json:"commitId"`
	FilePath        string `json:"filePath"`
	Line            int    `json:"line"`
	Body            string `json:"body"`
	ParentCommentID string `json:"parentCommentId,omitempty"`
}

// CreateComment posts a comment or reply on a review.
func (c *PlatformClient) CreateComment(ctx context.Context, reviewID string, params PlatformCreateCommentParams) (PlatformComment, error) {
	var comment PlatformComment
	err := c.do(ctx, http.MethodPost, "/v1/reviews/"+url.PathEscape(reviewID)+"/comments", params, true, &comment)
	return comment, err
}

type PlatformUpdateCommentStatusParams struct {
	Status string `json:"status"`
}

// UpdateCommentStatus sets a thread's status; the backend refuses it on a reply.
func (c *PlatformClient) UpdateCommentStatus(ctx context.Context, reviewID, commentID string, params PlatformUpdateCommentStatusParams) (PlatformComment, error) {
	var comment PlatformComment
	err := c.do(ctx, http.MethodPatch, "/v1/reviews/"+url.PathEscape(reviewID)+"/comments/"+url.PathEscape(commentID)+"/status", params, true, &comment)
	return comment, err
}

func (c *PlatformClient) ListReviewers(ctx context.Context, reviewID string) ([]PlatformReviewer, error) {
	var reviewers []PlatformReviewer
	err := c.do(ctx, http.MethodGet, "/v1/reviews/"+url.PathEscape(reviewID)+"/reviewers", nil, true, &reviewers)
	return reviewers, err
}

type PlatformAddReviewerParams struct {
	UserID string `json:"userId"`
}

// AddReviewer assigns a reviewer; a duplicate is ErrPlatformConflict and a user
// outside the tenant is ErrPlatformNotFound.
func (c *PlatformClient) AddReviewer(ctx context.Context, reviewID string, params PlatformAddReviewerParams) (PlatformReviewer, error) {
	var reviewer PlatformReviewer
	err := c.do(ctx, http.MethodPost, "/v1/reviews/"+url.PathEscape(reviewID)+"/reviewers", params, true, &reviewer)
	return reviewer, err
}

func (c *PlatformClient) RemoveReviewer(ctx context.Context, reviewID, userID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/reviews/"+url.PathEscape(reviewID)+"/reviewers/"+url.PathEscape(userID), nil, true, nil)
}

func (c *PlatformClient) ListBuilds(ctx context.Context, reviewID string) ([]PlatformBuild, error) {
	var builds []PlatformBuild
	err := c.do(ctx, http.MethodGet, "/v1/reviews/"+url.PathEscape(reviewID)+"/builds", nil, true, &builds)
	return builds, err
}

// PlatformCreateBuildParams.Kind is RECORDED; GATE is refused for new builds.
type PlatformCreateBuildParams struct {
	CommitID      string `json:"commitId"`
	Kind          string `json:"kind,omitempty"`
	Version       string `json:"version"`
	Successful    bool   `json:"successful"`
	FailureDetail string `json:"failureDetail,omitempty"`
}

// CreateBuild records a build, moving the review to READY or FAILED and to
// MERGE if it heads the queue.
func (c *PlatformClient) CreateBuild(ctx context.Context, reviewID string, params PlatformCreateBuildParams) (PlatformBuild, error) {
	var build PlatformBuild
	err := c.do(ctx, http.MethodPost, "/v1/reviews/"+url.PathEscape(reviewID)+"/builds", params, true, &build)
	return build, err
}

// ReviewFilterTraceDetails renders set filter fields in a fixed order for deterministic traces.
func ReviewFilterTraceDetails(filter PlatformReviewFilter) []string {
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

func FormatCommentLine(line int) string {
	return strconv.Itoa(line)
}
