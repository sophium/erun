package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
	eruncommon "github.com/sophium/erun/erun-common"
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
	// AdvanceMergeQueue and OverrideAdvanceMergeQueue act on one repository's
	// queue. An empty repository is one queue spanning every repository the
	// tenant serves, which is what a target branch alone has always meant.
	AdvanceMergeQueue(ctx context.Context, repository, targetBranch string) (model.Review, error)
	// OverrideAdvanceMergeQueue is the one deliberate, audited escape from
	// AdvanceMergeQueue's unresolved-thread gate: it refuses a blank reason and
	// fails closed if audit logging is not configured, rather than promoting
	// anyway.
	OverrideAdvanceMergeQueue(ctx context.Context, repository, targetBranch, reason string) (model.Review, error)
	// UpdateStatus applies a caller-reported status transition. remoteURL is
	// used only when status is MERGED: the remote the caller pushed the merge
	// to, fetched to verify the reported build's commit against the real
	// repository — see AGENTS.md "Merge Queue".
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
	// A distinct path (rather than a `force` flag on /advance) so a tenant can
	// grant the override to a narrower set of roles than ordinary advance
	// through the same permission-by-path mechanism every other route uses —
	// see AGENTS.md "Merge Queue".
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
	// RemoteURL is required only for a MERGED report: the git remote the
	// caller pushed the merge to, which the platform fetches to verify the
	// reported build's commit against the real repository.
	RemoteURL string `json:"remoteUrl"`
}

// advanceMergeQueueRequest addresses one repository's queue. An empty
// repository is a queue spanning every repository the tenant serves, which is
// the pre-repository-identity meaning of a target branch alone; erun's own
// clients always name the repository they are standing in.
type advanceMergeQueueRequest struct {
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
}

type overrideAdvanceMergeQueueRequest struct {
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
	Reason       string `json:"reason"`
}

// unresolvedThreadsResponse is what a blocked /merge-queue/advance reports: not
// just that it refused, but how many threads and on which review, so a caller
// can route the operator straight to them instead of a dead end.
type unresolvedThreadsResponse struct {
	Error             string `json:"error"`
	Message           string `json:"message"`
	ReviewID          string `json:"reviewId"`
	UnresolvedThreads int    `json:"unresolvedThreads"`
}

type addReviewerRequest struct {
	UserID string `json:"userId"`
}

// listReviews answers GET /v1/reviews. The `?status=` filter is normalized and
// then validated before it reaches the repository: an unrecognised value
// matches no row, so passing one through answered `200` with an empty list a
// caller reads as "no reviews" -- the conclusion an operator acts on when they
// check whether anything is waiting on them. The membership check is the shared
// eruncommon.NormalizeReviewStatus, the same refusal `erun review list` makes
// before it ever calls the platform, so both surfaces accept the same
// spellings and name the same accepted values.
func (r ReviewRoutes) listReviews(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	status := model.ReviewStatus(strings.ToUpper(strings.TrimSpace(query.Get("status"))))
	if _, err := eruncommon.NormalizeReviewStatus(string(status)); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_QUERY", err.Error())
		return
	}
	filter := apirepository.ReviewFilter{
		Repository:     query.Get("repository"),
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

func (r ReviewRoutes) createReview(w http.ResponseWriter, req *http.Request) {
	var review model.Review
	if err := decodeJSON(req, &review); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	prepared, err := r.service.PrepareCreate(review)
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_REPOSITORY", err.Error())
		return
	}
	review = prepared
	review, err = r.reviews.Create(req.Context(), review)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, withReviewIssueRef(review))
}

func (r ReviewRoutes) listMergeQueue(w http.ResponseWriter, req *http.Request) {
	reviews, err := r.reviews.ListMergeQueue(req.Context(), req.URL.Query().Get("repository"), req.URL.Query().Get("targetBranch"))
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
	review, err := r.service.AdvanceMergeQueue(req.Context(), input.Repository, input.TargetBranch)
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
	review, err := r.service.OverrideAdvanceMergeQueue(req.Context(), input.Repository, input.TargetBranch, input.Reason)
	if err != nil {
		writeAdvanceMergeQueueError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, withReviewIssueRef(review))
}

// writeAdvanceMergeQueueError reports an unresolved-thread block as a 409 with
// the count and the review to route the operator to, rather than the bare
// status text writeRepositoryError gives every other conflict, and gives the
// merge-queue-specific machine codes documented in collaboration/reviews.md.
// An occupied MERGE slot is the same shape of refusal for the same reason:
// naming the review already holding the branch is what makes the advance worth
// retrying, so it gets its own code rather than the generic not-found it would
// otherwise fall through to.
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
		writeErrorDetails(w, http.StatusConflict, "MERGE_QUEUE_AMBIGUOUS", ambiguous.Error(), map[string]any{
			"targetBranch": ambiguous.TargetBranch,
			"repositories": ambiguous.Repositories,
		})
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

// writeUpdateStatusError gives PATCH .../status's documented business codes
// their exact machine code and details shape; every other failure (not
// found, missing security context, ...) falls through to the generic
// status-derived code.
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
