package model

import "time"

type CommentStatus string

const (
	CommentStatusOpen   CommentStatus = "OPEN"
	CommentStatusClosed CommentStatus = "CLOSED"
)

type Comment struct {
	CommentID       string        `json:"commentId"`
	TenantID        string        `json:"tenantId"`
	ReviewID        string        `json:"reviewId"`
	CreatorUserID   string        `json:"creatorUserId,omitempty"`
	Status          CommentStatus `json:"status"`
	ParentCommentID string        `json:"parentCommentId,omitempty"`
	CommitID        string        `json:"commitId"`
	Line            int           `json:"line"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
}
