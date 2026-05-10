package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type CommentRepository interface {
	Create(ctx context.Context, comment model.Comment) (model.Comment, error)
	List(ctx context.Context, filter apirepository.CommentFilter) ([]model.Comment, error)
}

type CommentService interface {
	PrepareCreate(ctx context.Context, comment model.Comment) (model.Comment, error)
	UpdateStatus(ctx context.Context, commentID string, status model.CommentStatus) (model.Comment, error)
}

type CommentRoutes struct {
	comments CommentRepository
	service  CommentService
}

func RegisterCommentRoutes(register ProtectedRouteRegistrar, comments CommentRepository, service CommentService) {
	routes := CommentRoutes{comments: comments, service: service}
	register(http.MethodGet, "/v1/reviews/{review_id}/comments", http.HandlerFunc(routes.listComments))
	register(http.MethodPost, "/v1/reviews/{review_id}/comments", http.HandlerFunc(routes.createComment))
	register(http.MethodPatch, "/v1/reviews/{review_id}/comments/{comment_id}/status", http.HandlerFunc(routes.updateCommentStatus))
}

type updateCommentStatusRequest struct {
	Status model.CommentStatus `json:"status"`
}

func (r CommentRoutes) listComments(w http.ResponseWriter, req *http.Request) {
	comments, err := r.comments.List(req.Context(), apirepository.CommentFilter{ReviewID: req.PathValue("review_id")})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, comments)
}

func (r CommentRoutes) createComment(w http.ResponseWriter, req *http.Request) {
	var comment model.Comment
	if err := decodeJSON(req, &comment); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	comment.ReviewID = req.PathValue("review_id")
	comment, err := r.service.PrepareCreate(req.Context(), comment)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	comment, err = r.comments.Create(req.Context(), comment)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

func (r CommentRoutes) updateCommentStatus(w http.ResponseWriter, req *http.Request) {
	var input updateCommentStatusRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	comment, err := r.service.UpdateStatus(req.Context(), req.PathValue("comment_id"), input.Status)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, comment)
}
