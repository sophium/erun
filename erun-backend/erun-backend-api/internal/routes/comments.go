package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type CommentRepository interface {
	Create(ctx context.Context, comment model.Comment) (model.Comment, error)
	ListByReview(ctx context.Context, reviewID string) ([]model.Comment, error)
	UpdateStatus(ctx context.Context, reviewID string, commentID string, status model.CommentStatus) (model.Comment, error)
}

type CommentRoutes struct {
	comments CommentRepository
}

func RegisterCommentRoutes(register ProtectedRouteRegistrar, comments CommentRepository) {
	routes := CommentRoutes{comments: comments}
	register(http.MethodGet, "/v1/reviews/{review_id}/comments", http.HandlerFunc(routes.listComments))
	register(http.MethodPost, "/v1/reviews/{review_id}/comments", http.HandlerFunc(routes.createComment))
	register(http.MethodPatch, "/v1/reviews/{review_id}/comments/{comment_id}/status", http.HandlerFunc(routes.updateCommentStatus))
}

type createCommentRequest struct {
	ParentCommentID string `json:"parentCommentId"`
	CommitID        string `json:"commitId"`
	Line            int    `json:"line"`
}

type updateCommentStatusRequest struct {
	Status model.CommentStatus `json:"status"`
}

func (r CommentRoutes) listComments(w http.ResponseWriter, req *http.Request) {
	comments, err := r.comments.ListByReview(req.Context(), req.PathValue("review_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, comments)
}

func (r CommentRoutes) createComment(w http.ResponseWriter, req *http.Request) {
	var input createCommentRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	comment, err := r.comments.Create(req.Context(), model.Comment{
		ReviewID:        req.PathValue("review_id"),
		Status:          model.CommentStatusOpen,
		ParentCommentID: input.ParentCommentID,
		CommitID:        input.CommitID,
		Line:            input.Line,
	})
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
	comment, err := r.comments.UpdateStatus(req.Context(), req.PathValue("review_id"), req.PathValue("comment_id"), input.Status)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, comment)
}
