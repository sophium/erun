package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type ReviewRepository interface {
	Create(ctx context.Context, review model.Review) (model.Review, error)
	Get(ctx context.Context, reviewID string) (model.Review, error)
	List(ctx context.Context, targetBranch string) ([]model.Review, error)
	ListMergeQueue(ctx context.Context, targetBranch string) ([]model.Review, error)
	AdvanceMergeQueue(ctx context.Context, targetBranch string) (model.Review, error)
	UpdateStatus(ctx context.Context, reviewID string, status model.ReviewStatus, buildID string) (model.Review, error)
}

type ReviewRoutes struct {
	reviews ReviewRepository
}

func RegisterReviewRoutes(register ProtectedRouteRegistrar, reviews ReviewRepository) {
	routes := ReviewRoutes{reviews: reviews}
	register(http.MethodGet, "/v1/reviews", http.HandlerFunc(routes.listReviews))
	register(http.MethodPost, "/v1/reviews", http.HandlerFunc(routes.createReview))
	register(http.MethodGet, "/v1/reviews/merge-queue", http.HandlerFunc(routes.listMergeQueue))
	register(http.MethodPost, "/v1/reviews/merge-queue/advance", http.HandlerFunc(routes.advanceMergeQueue))
	register(http.MethodGet, "/v1/reviews/{review_id}", http.HandlerFunc(routes.getReview))
	register(http.MethodPatch, "/v1/reviews/{review_id}/status", http.HandlerFunc(routes.updateReviewStatus))
}

type createReviewRequest struct {
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
}

type updateReviewStatusRequest struct {
	Status  model.ReviewStatus `json:"status"`
	BuildID string             `json:"buildId"`
}

type advanceMergeQueueRequest struct {
	TargetBranch string `json:"targetBranch"`
}

func (r ReviewRoutes) listReviews(w http.ResponseWriter, req *http.Request) {
	reviews, err := r.reviews.List(req.Context(), req.URL.Query().Get("targetBranch"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reviews)
}

func (r ReviewRoutes) createReview(w http.ResponseWriter, req *http.Request) {
	var input createReviewRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	review, err := r.reviews.Create(req.Context(), model.Review{
		Name:         input.Name,
		TargetBranch: input.TargetBranch,
		SourceBranch: input.SourceBranch,
		Status:       model.ReviewStatusOpen,
	})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, review)
}

func (r ReviewRoutes) listMergeQueue(w http.ResponseWriter, req *http.Request) {
	reviews, err := r.reviews.ListMergeQueue(req.Context(), req.URL.Query().Get("targetBranch"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reviews)
}

func (r ReviewRoutes) advanceMergeQueue(w http.ResponseWriter, req *http.Request) {
	var input advanceMergeQueueRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	review, err := r.reviews.AdvanceMergeQueue(req.Context(), input.TargetBranch)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func (r ReviewRoutes) getReview(w http.ResponseWriter, req *http.Request) {
	review, err := r.reviews.Get(req.Context(), req.PathValue("review_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func (r ReviewRoutes) updateReviewStatus(w http.ResponseWriter, req *http.Request) {
	var input updateReviewStatusRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	review, err := r.reviews.UpdateStatus(req.Context(), req.PathValue("review_id"), input.Status, input.BuildID)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}
