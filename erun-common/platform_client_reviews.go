package eruncommon

import (
	"context"
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

// PlatformBuild mirrors model.Build's JSON shape.
type PlatformBuild struct {
	BuildID    string    `json:"buildId"`
	TenantID   string    `json:"tenantId"`
	ReviewID   string    `json:"reviewId"`
	ReviewName string    `json:"reviewName,omitempty"`
	Successful bool      `json:"successful"`
	CommitID   string    `json:"commitId"`
	Version    string    `json:"version"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
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
type PlatformUpdateReviewStatusParams struct {
	Status  string `json:"status"`
	BuildID string `json:"buildId,omitempty"`
}

// UpdateReviewStatus transitions a review's status (e.g. to CLOSED).
func (c *PlatformClient) UpdateReviewStatus(ctx context.Context, reviewID string, params PlatformUpdateReviewStatusParams) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPatch, "/v1/reviews/"+url.PathEscape(reviewID)+"/status", params, true, &review)
	return review, err
}

// ListMergeQueue lists the reviews queued (or already READY) to merge into
// targetBranch, in queue order.
func (c *PlatformClient) ListMergeQueue(ctx context.Context, targetBranch string) ([]PlatformReview, error) {
	path := "/v1/reviews/merge-queue?targetBranch=" + url.QueryEscape(targetBranch)
	var reviews []PlatformReview
	err := c.do(ctx, http.MethodGet, path, nil, true, &reviews)
	return reviews, err
}

// AdvanceMergeQueue advances targetBranch's merge queue head to MERGED.
func (c *PlatformClient) AdvanceMergeQueue(ctx context.Context, targetBranch string) (PlatformReview, error) {
	var review PlatformReview
	err := c.do(ctx, http.MethodPost, "/v1/reviews/merge-queue/advance", map[string]string{"targetBranch": targetBranch}, true, &review)
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

// ListBuilds lists the builds recorded against a review.
func (c *PlatformClient) ListBuilds(ctx context.Context, reviewID string) ([]PlatformBuild, error) {
	var builds []PlatformBuild
	err := c.do(ctx, http.MethodGet, "/v1/reviews/"+url.PathEscape(reviewID)+"/builds", nil, true, &builds)
	return builds, err
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
