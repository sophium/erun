package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type ReviewRepository interface {
	Create(ctx context.Context, review model.Review) (model.Review, error)
	Get(ctx context.Context, reviewID string) (model.Review, error)
	List(ctx context.Context, filter apirepository.ReviewFilter) ([]model.Review, error)
	ListMergeQueue(ctx context.Context, targetBranch string) ([]model.Review, error)
}

type ReviewReviewerRepository interface {
	Create(ctx context.Context, reviewer model.ReviewReviewer) (model.ReviewReviewer, error)
	List(ctx context.Context, filter apirepository.ReviewReviewerFilter) ([]model.ReviewReviewer, error)
	Delete(ctx context.Context, reviewID, userID string) error
}

type ReviewService interface {
	PrepareCreate(review model.Review) model.Review
	AdvanceMergeQueue(ctx context.Context, targetBranch string) (model.Review, error)
	UpdateStatus(ctx context.Context, reviewID string, status model.ReviewStatus, buildID string) (model.Review, error)
}

type ReviewRoutes struct {
	reviews   ReviewRepository
	reviewers ReviewReviewerRepository
	service   ReviewService
	// dispatcher starts the merge queue's gate build for whichever review the
	// manual /merge-queue/advance call promotes. Nil when the merge executor is
	// not wired: the review still moves to MERGE, just with nothing to advance
	// it further until an operator does so by hand.
	dispatcher service.MergeQueueDispatcher
}

func RegisterReviewRoutes(register ProtectedRouteRegistrar, reviews ReviewRepository, reviewers ReviewReviewerRepository, reviewService ReviewService, dispatcher service.MergeQueueDispatcher) {
	routes := ReviewRoutes{reviews: reviews, reviewers: reviewers, service: reviewService, dispatcher: dispatcher}
	register(http.MethodGet, "/v1/reviews", http.HandlerFunc(routes.listReviews))
	register(http.MethodPost, "/v1/reviews", http.HandlerFunc(routes.createReview))
	register(http.MethodGet, "/v1/reviews/merge-queue", http.HandlerFunc(routes.listMergeQueue))
	register(http.MethodPost, "/v1/reviews/merge-queue/advance", http.HandlerFunc(routes.advanceMergeQueue))
	register(http.MethodGet, "/v1/reviews/{review_id}", http.HandlerFunc(routes.getReview))
	register(http.MethodPatch, "/v1/reviews/{review_id}/status", http.HandlerFunc(routes.updateReviewStatus))
	register(http.MethodGet, "/v1/reviews/{review_id}/reviewers", http.HandlerFunc(routes.listReviewers))
	register(http.MethodPost, "/v1/reviews/{review_id}/reviewers", http.HandlerFunc(routes.addReviewer))
	register(http.MethodDelete, "/v1/reviews/{review_id}/reviewers/{user_id}", http.HandlerFunc(routes.removeReviewer))
}

type updateReviewStatusRequest struct {
	Status  model.ReviewStatus `json:"status"`
	BuildID string             `json:"buildId"`
}

type advanceMergeQueueRequest struct {
	TargetBranch string `json:"targetBranch"`
}

type addReviewerRequest struct {
	UserID string `json:"userId"`
}

func (r ReviewRoutes) listReviews(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	filter := apirepository.ReviewFilter{
		TargetBranch:   query.Get("targetBranch"),
		SourceBranch:   query.Get("sourceBranch"),
		Status:         model.ReviewStatus(query.Get("status")),
		AuthorUserID:   query.Get("authorUserId"),
		ReviewerUserID: query.Get("reviewerUserId"),
	}
	reviews, err := r.reviews.List(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reviews)
}

func (r ReviewRoutes) listReviewers(w http.ResponseWriter, req *http.Request) {
	reviewers, err := r.reviewers.List(req.Context(), apirepository.ReviewReviewerFilter{ReviewID: req.PathValue("review_id")})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reviewers)
}

func (r ReviewRoutes) addReviewer(w http.ResponseWriter, req *http.Request) {
	var input addReviewerRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reviewer, err := r.reviewers.Create(req.Context(), model.ReviewReviewer{
		ReviewID: req.PathValue("review_id"),
		UserID:   input.UserID,
	})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, reviewer)
}

func (r ReviewRoutes) removeReviewer(w http.ResponseWriter, req *http.Request) {
	if err := r.reviewers.Delete(req.Context(), req.PathValue("review_id"), req.PathValue("user_id")); err != nil {
		writeRepositoryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r ReviewRoutes) createReview(w http.ResponseWriter, req *http.Request) {
	var review model.Review
	if err := decodeJSON(req, &review); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	review = r.service.PrepareCreate(review)
	review, err := r.reviews.Create(req.Context(), review)
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
	review, err := r.service.AdvanceMergeQueue(req.Context(), input.TargetBranch)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if r.dispatcher != nil {
		r.dispatcher.Dispatch(req.Context(), review)
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
	review, err := r.service.UpdateStatus(req.Context(), req.PathValue("review_id"), input.Status, input.BuildID)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}
