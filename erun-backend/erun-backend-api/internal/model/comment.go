package model

import (
	"time"

	"github.com/uptrace/bun"
)

type CommentStatus string

const (
	CommentStatusOpen   CommentStatus = "OPEN"
	CommentStatusClosed CommentStatus = "CLOSED"
)

type Comment struct {
	bun.BaseModel   `bun:"table:comments,alias:c"`
	CommentID       string        `json:"commentId" bun:"comment_id,pk,scanonly"`
	TenantID        string        `json:"tenantId" bun:"tenant_id,scanonly"`
	ReviewID        string        `json:"reviewId" bun:"review_id"`
	CreatorUserID   string        `json:"creatorUserId,omitempty" bun:"creator_user_id,nullzero"`
	Status          CommentStatus `json:"status" bun:"status"`
	ParentCommentID string        `json:"parentCommentId,omitempty" bun:"parent_comment_id,nullzero"`
	CommitID        string        `json:"commitId" bun:"commit_id"`
	Line            int           `json:"line" bun:"line"`
	CreatedAt       time.Time     `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt       time.Time     `json:"updatedAt" bun:"updated_at,scanonly"`
}
