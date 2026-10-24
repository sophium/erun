package model

import (
	"net/url"
	"strings"
	"time"
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
	BuildID         string               `json:"buildId"`
	TenantID        string               `json:"tenantId"`
	ReviewID        string               `json:"reviewId,omitempty"`
	ReviewName      string               `json:"reviewName,omitempty"`
	EnvironmentID   string               `json:"environmentId,omitempty"`
	EnvironmentName string               `json:"environmentName,omitempty"`
	Successful      bool                 `json:"successful"`
	CommitID        string               `json:"commitId"`
	Version         string               `json:"version"`
	FailureDetail   string               `json:"failureDetail,omitempty"`
	Profile         *BuildProfileSummary `json:"profile,omitempty"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt       time.Time            `json:"updatedAt"`
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

// PlatformMergeQueueParams addresses a merge queue; empty fields list all.
type PlatformMergeQueueParams struct {
	Repository   string
	TargetBranch string
}

func (p PlatformMergeQueueParams) QueryString() string {
	values := url.Values{}
	if strings.TrimSpace(p.TargetBranch) != "" {
		values.Set("targetBranch", p.TargetBranch)
	}
	if strings.TrimSpace(p.Repository) != "" {
		values.Set("repository", p.Repository)
	}
	return values.Encode()
}
