package service

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

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
	if comment.Status == "" {
		comment.Status = model.CommentStatusOpen
	}
	if comment.ParentCommentID == "" {
		comment.CreatorUserID = securityContext.ErunUserID
	} else {
		comment.CreatorUserID = ""
	}
	return comment, nil
}

func (s *CommentService) UpdateStatus(ctx context.Context, commentID string, status model.CommentStatus) (model.Comment, error) {
	comment, err := s.comments.Get(ctx, commentID)
	if err != nil {
		return model.Comment{}, err
	}
	comment.Status = status
	return s.comments.Update(ctx, comment)
}
