package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

type ReviewRepository interface {
	Create(ctx context.Context, review model.Review) (model.Review, error)
	Get(ctx context.Context, reviewID string) (model.Review, error)
	List(ctx context.Context, filter apirepository.ReviewFilter) ([]model.Review, error)
	ListMergeQueue(ctx context.Context, repository, targetBranch string) ([]model.Review, error)
}

type ReviewReviewerRepository interface {
	Create(ctx context.Context, reviewer model.ReviewReviewer) (model.ReviewReviewer, error)
	List(ctx context.Context, filter apirepository.ReviewReviewerFilter) ([]model.ReviewReviewer, error)
	Delete(ctx context.Context, reviewID, userID string) error
}

type ReviewService interface {
	// PrepareCreate normalizes a new review's status and repository identity,
	// refusing a repository it cannot canonicalize.
	PrepareCreate(review model.Review) (model.Review, error)
	// AdvanceMergeQueue treats an empty repository as one queue spanning every repository the tenant serves.
	AdvanceMergeQueue(ctx context.Context, repository, targetBranch string) (model.Review, error)
	// OverrideAdvanceMergeQueue bypasses the unresolved-thread gate; it requires a reason and fails
	// closed without audit logging.
	OverrideAdvanceMergeQueue(ctx context.Context, repository, targetBranch, reason string) (model.Review, error)
	// UpdateStatus uses remoteURL only for MERGED, to verify the reported build's commit against the real repository.
	UpdateStatus(ctx context.Context, reviewID string, status model.ReviewStatus, buildID string, remoteURL string) (model.Review, error)
}

type ReviewRoutes struct {
	reviews   ReviewRepository
	reviewers ReviewReviewerRepository
	service   ReviewService
}

func RegisterReviewRoutes(register ProtectedRouteRegistrar, reviews ReviewRepository, reviewers ReviewReviewerRepository, reviewService ReviewService) {
	routes := ReviewRoutes{reviews: reviews, reviewers: reviewers, service: reviewService}
	register(http.MethodGet, "/v1/reviews", http.HandlerFunc(routes.listReviews))
	register(http.MethodPost, "/v1/reviews", http.HandlerFunc(routes.createReview))
	register(http.MethodGet, "/v1/reviews/merge-queue", http.HandlerFunc(routes.listMergeQueue))
	register(http.MethodPost, "/v1/reviews/merge-queue/advance", http.HandlerFunc(routes.advanceMergeQueue))
	// A distinct path, not a flag, so the override can be granted to fewer roles.
	register(http.MethodPost, "/v1/reviews/merge-queue/override-advance", http.HandlerFunc(routes.overrideAdvanceMergeQueue))
	register(http.MethodGet, "/v1/reviews/{review_id}", http.HandlerFunc(routes.getReview))
	register(http.MethodPatch, "/v1/reviews/{review_id}/status", http.HandlerFunc(routes.updateReviewStatus))
	register(http.MethodGet, "/v1/reviews/{review_id}/reviewers", http.HandlerFunc(routes.listReviewers))
	register(http.MethodPost, "/v1/reviews/{review_id}/reviewers", http.HandlerFunc(routes.addReviewer))
	register(http.MethodDelete, "/v1/reviews/{review_id}/reviewers/{user_id}", http.HandlerFunc(routes.removeReviewer))
}

type updateReviewStatusRequest struct {
	Status  model.ReviewStatus `json:"status"`
	BuildID string             `json:"buildId"`
	// RemoteURL is required only for a MERGED report.
	RemoteURL string `json:"remoteUrl"`
}

type advanceMergeQueueRequest struct {
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
}

type overrideAdvanceMergeQueueRequest struct {
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
	Reason       string `json:"reason"`
}

type unresolvedThreadsResponse struct {
	Error             string `json:"error"`
	Message           string `json:"message"`
	ReviewID          string `json:"reviewId"`
	UnresolvedThreads int    `json:"unresolvedThreads"`
}

type addReviewerRequest struct {
	UserID string `json:"userId"`
}

// createReviewRequest is deliberately not model.Review: IssueRefSource and Status are platform-owned
// and must have nowhere on the wire to arrive from.
type createReviewRequest struct {
	Repository   string `json:"repository"`
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	// IssueRef is owner/repo#number or a bare number joined to the review's repository.
	IssueRef string `json:"issueRef"`
}

// canonicalRepositoryParam applies PrepareCreate's canonicalization so every remote spelling reaches
// the same rows; an unusable value is refused rather than answered as empty.
func canonicalRepositoryParam(repository string) (string, error) {
	if strings.TrimSpace(repository) == "" {
		return "", nil
	}
	identity, err := modelpkg.RepositoryIdentity(repository)
	if err != nil {
		return "", err
	}
	return identity, nil
}

// An unrecognised status must be refused, not answered as an empty list.
func (r ReviewRoutes) listReviews(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	status := model.ReviewStatus(strings.ToUpper(strings.TrimSpace(query.Get("status"))))
	if _, err := modelpkg.NormalizeReviewStatus(string(status)); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_QUERY", err.Error())
		return
	}
	repository, err := canonicalRepositoryParam(query.Get("repository"))
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_REPOSITORY", err.Error())
		return
	}
	filter := apirepository.ReviewFilter{
		Repository:     repository,
		TargetBranch:   query.Get("targetBranch"),
		SourceBranch:   query.Get("sourceBranch"),
		Status:         status,
		AuthorUserID:   query.Get("authorUserId"),
		ReviewerUserID: query.Get("reviewerUserId"),
	}
	reviews, err := r.reviews.List(req.Context(), filter)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, withReviewsIssueRef(reviews))
}

func (r ReviewRoutes) listReviewers(w http.ResponseWriter, req *http.Request) {
	reviewers, err := r.reviewers.List(req.Context(), apirepository.ReviewReviewerFilter{ReviewID: req.PathValue("review_id")})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, reviewers)
}

func (r ReviewRoutes) addReviewer(w http.ResponseWriter, req *http.Request) {
	var input addReviewerRequest
	if err := decodeJSON(req, &input); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	reviewer, err := r.reviewers.Create(req.Context(), model.ReviewReviewer{
		ReviewID: req.PathValue("review_id"),
		UserID:   input.UserID,
	})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, reviewer)
}

func (r ReviewRoutes) removeReviewer(w http.ResponseWriter, req *http.Request) {
	if err := r.reviewers.Delete(req.Context(), req.PathValue("review_id"), req.PathValue("user_id")); err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeReviewPrepareError(w http.ResponseWriter, err error) {
	var invalidIssueRef *service.InvalidIssueRefError
	if errors.As(err, &invalidIssueRef) {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_ISSUE_REF", invalidIssueRef.Error())
		return
	}
	writeErrorCode(w, http.StatusBadRequest, "INVALID_REPOSITORY", err.Error())
}

func (r ReviewRoutes) createReview(w http.ResponseWriter, req *http.Request) {
	var input createReviewRequest
	if err := decodeJSON(req, &input); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	// Only these request fields reach a review; platform-owned fields start zeroed.
	prepared, err := r.service.PrepareCreate(model.Review{
		Repository:       input.Repository,
		Name:             input.Name,
		TargetBranch:     input.TargetBranch,
		SourceBranch:     input.SourceBranch,
		DeclaredIssueRef: input.IssueRef,
	})
	if err != nil {
		writeReviewPrepareError(w, err)
		return
	}
	review, err := r.reviews.Create(req.Context(), prepared)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, withReviewIssueRef(review))
}

func (r ReviewRoutes) listMergeQueue(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	repository, err := canonicalRepositoryParam(query.Get("repository"))
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_REPOSITORY", err.Error())
		return
	}
	reviews, err := r.reviews.ListMergeQueue(req.Context(), repository, query.Get("targetBranch"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, withReviewsIssueRef(reviews))
}

func (r ReviewRoutes) advanceMergeQueue(w http.ResponseWriter, req *http.Request) {
	var input advanceMergeQueueRequest
	if err := decodeJSON(req, &input); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	repository, err := canonicalRepositoryParam(input.Repository)
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_REPOSITORY", err.Error())
		return
	}
	review, err := r.service.AdvanceMergeQueue(req.Context(), repository, input.TargetBranch)
	if err != nil {
		writeAdvanceMergeQueueError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, withReviewIssueRef(review))
}

func (r ReviewRoutes) overrideAdvanceMergeQueue(w http.ResponseWriter, req *http.Request) {
	var input overrideAdvanceMergeQueueRequest
	if err := decodeJSON(req, &input); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	repository, err := canonicalRepositoryParam(input.Repository)
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_REPOSITORY", err.Error())
		return
	}
	review, err := r.service.OverrideAdvanceMergeQueue(req.Context(), repository, input.TargetBranch, input.Reason)
	if err != nil {
		writeAdvanceMergeQueueError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, withReviewIssueRef(review))
}

func writeAdvanceMergeQueueError(w http.ResponseWriter, req *http.Request, err error) {
	var occupied *service.MergeQueueOccupiedError
	if errors.As(err, &occupied) {
		writeErrorDetails(w, http.StatusConflict, "MERGE_QUEUE_OCCUPIED", occupied.Error(), map[string]any{
			"targetBranch": occupied.TargetBranch,
			"reviewId":     occupied.ReviewID,
			"name":         occupied.Name,
			"sourceBranch": occupied.SourceBranch,
		})
		return
	}
	var blocked *service.UnresolvedThreadsError
	if errors.As(err, &blocked) {
		writeJSON(w, http.StatusConflict, unresolvedThreadsResponse{
			Error:             "unresolved_threads",
			Message:           blocked.Error(),
			ReviewID:          blocked.ReviewID,
			UnresolvedThreads: blocked.UnresolvedThreads,
		})
		return
	}
	if errors.Is(err, service.ErrInvalidTargetBranch) {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_TARGET_BRANCH", err.Error())
		return
	}
	var ambiguous *service.AmbiguousMergeQueueError
	if errors.As(err, &ambiguous) {
		details := map[string]any{
			"targetBranch": ambiguous.TargetBranch,
			"repositories": ambiguous.Repositories,
		}
		unrecorded := ambiguous.UnrecordedReviewIDs
		if unrecorded == nil {
			// An empty array, not null: "none" differs from "the platform cannot say".
			unrecorded = []string{}
		}
		details["unrecordedRepositoryReviewIds"] = unrecorded
		writeErrorDetails(w, http.StatusConflict, "MERGE_QUEUE_AMBIGUOUS", ambiguous.Error(), details)
		return
	}
	var empty *service.EmptyMergeQueueError
	if errors.As(err, &empty) {
		writeErrorCode(w, http.StatusNotFound, "EMPTY_QUEUE", empty.Error())
		return
	}
	writeRepositoryError(w, req, err)
}

func (r ReviewRoutes) getReview(w http.ResponseWriter, req *http.Request) {
	review, err := r.reviews.Get(req.Context(), req.PathValue("review_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, withReviewIssueRef(review))
}

func (r ReviewRoutes) updateReviewStatus(w http.ResponseWriter, req *http.Request) {
	var input updateReviewStatusRequest
	if err := decodeJSON(req, &input); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	review, err := r.service.UpdateStatus(req.Context(), req.PathValue("review_id"), input.Status, input.BuildID, input.RemoteURL)
	if err != nil {
		writeUpdateStatusError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, withReviewIssueRef(review))
}

func writeUpdateStatusError(w http.ResponseWriter, req *http.Request, err error) {
	var invalidTransition *service.InvalidTransitionError
	if errors.As(err, &invalidTransition) {
		writeErrorDetails(w, http.StatusBadRequest, "INVALID_TRANSITION", invalidTransition.Error(), map[string]any{
			"from":         invalidTransition.From,
			"to":           invalidTransition.To,
			"validTargets": invalidTransition.ValidTargets,
		})
		return
	}
	var missingBuildID *service.MissingBuildIDError
	if errors.As(err, &missingBuildID) {
		writeErrorDetails(w, http.StatusBadRequest, "INVALID_BODY", missingBuildID.Error(), map[string]any{"field": "buildId"})
		return
	}
	var notVerified *service.MergeNotVerifiedError
	if errors.As(err, &notVerified) {
		writeErrorCode(w, http.StatusConflict, "MERGE_NOT_VERIFIED", notVerified.Error())
		return
	}
	var notMerging *service.ReviewNotMergingError
	if errors.As(err, &notMerging) {
		writeErrorDetails(w, http.StatusConflict, "REVIEW_NOT_MERGING", notMerging.Error(), map[string]any{
			"reviewId": notMerging.ReviewID,
			"status":   notMerging.Status,
		})
		return
	}
	writeRepositoryError(w, req, err)
}
