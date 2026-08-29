package service

import (
	"context"
	"fmt"
	"regexp"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// commitIDPattern is the 40-lowercase-hex-character grammar documented in
// collaboration/comments.md's Validation rules table.
var commitIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// InvalidCommitIDError refuses a comment whose commitId is not 40 lowercase
// hex characters.
type InvalidCommitIDError struct {
	CommitID string
}

func (e *InvalidCommitIDError) Error() string {
	return fmt.Sprintf("commitId %q is not 40 lowercase hex characters", e.CommitID)
}

func (e *InvalidCommitIDError) Unwrap() error { return repository.ErrInvalidInput }

// AlreadyClosedError refuses closing a comment thread that is already closed.
type AlreadyClosedError struct {
	CommentID string
}

func (e *AlreadyClosedError) Error() string {
	return fmt.Sprintf("comment %s is already closed", e.CommentID)
}

func (e *AlreadyClosedError) Unwrap() error { return repository.ErrConflict }

type CommentRepository interface {
	Create(ctx context.Context, comment model.Comment) (model.Comment, error)
	Get(ctx context.Context, commentID string) (model.Comment, error)
	Update(ctx context.Context, comment model.Comment) (model.Comment, error)
}

type CommentService struct {
	comments CommentRepository
}

func NewCommentService(comments CommentRepository) *CommentService {
	return &CommentService{comments: comments}
}

func (s *CommentService) PrepareCreate(ctx context.Context, comment model.Comment) (model.Comment, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return model.Comment{}, repository.ErrMissingSecurityContext
	}
	if !commitIDPattern.MatchString(comment.CommitID) {
		return model.Comment{}, &InvalidCommitIDError{CommitID: comment.CommitID}
	}
	if comment.Status == "" {
		comment.Status = model.CommentStatusOpen
	}
	comment.CreatorUserID = securityContext.ErunUserID
	return comment, nil
}

func (s *CommentService) UpdateStatus(ctx context.Context, commentID string, status model.CommentStatus) (model.Comment, error) {
	comment, err := s.comments.Get(ctx, commentID)
	if err != nil {
		return model.Comment{}, err
	}
	if comment.Status == model.CommentStatusClosed && status == model.CommentStatusClosed {
		return model.Comment{}, &AlreadyClosedError{CommentID: commentID}
	}
	comment.Status = status
	return s.comments.Update(ctx, comment)
}
