package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

type stubReviewRepository struct {
	reviews   []model.Review
	err       error
	gotFilter apirepository.ReviewFilter
}

func (s *stubReviewRepository) Create(context.Context, model.Review) (model.Review, error) {
	return model.Review{}, s.err
}

func (s *stubReviewRepository) Get(context.Context, string) (model.Review, error) {
	return model.Review{}, s.err
}

func (s *stubReviewRepository) List(_ context.Context, filter apirepository.ReviewFilter) ([]model.Review, error) {
	s.gotFilter = filter
	return s.reviews, s.err
}

func (s *stubReviewRepository) ListMergeQueue(context.Context, string) ([]model.Review, error) {
	return s.reviews, s.err
}

type stubReviewReviewerRepository struct {
	reviewers       []model.ReviewReviewer
	created         model.ReviewReviewer
	deletedReviewID string
	deletedUserID   string
	err             error
}

func (s *stubReviewReviewerRepository) Create(_ context.Context, reviewer model.ReviewReviewer) (model.ReviewReviewer, error) {
	s.created = reviewer
	if s.err != nil {
		return model.ReviewReviewer{}, s.err
	}
	return reviewer, nil
}

func (s *stubReviewReviewerRepository) List(context.Context, apirepository.ReviewReviewerFilter) ([]model.ReviewReviewer, error) {
	return s.reviewers, s.err
}

func (s *stubReviewReviewerRepository) Delete(_ context.Context, reviewID, userID string) error {
	s.deletedReviewID = reviewID
	s.deletedUserID = userID
	return s.err
}

type stubReviewService struct {
	review model.Review
	err    error
}

func (s stubReviewService) PrepareCreate(review model.Review) model.Review { return review }

func (s stubReviewService) AdvanceMergeQueue(context.Context, string) (model.Review, error) {
	return s.review, s.err
}

func (s stubReviewService) UpdateStatus(context.Context, string, model.ReviewStatus, string) (model.Review, error) {
	return s.review, s.err
}

type recordingDispatcher struct {
	reviews []model.Review
}

func (d *recordingDispatcher) Dispatch(_ context.Context, review model.Review) {
	d.reviews = append(d.reviews, review)
}

func patchReviewStatus(t *testing.T, routes ReviewRoutes, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/v1/reviews/review-1/status", bytes.NewBufferString(body))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()
	routes.updateReviewStatus(rec, req)
	return rec
}

// TestUpdateReviewStatusReturnsTheServiceResult: PATCH .../status is a thin
// adapter over ReviewService.UpdateStatus, which is where MERGE/MERGED are
// refused (service.TestUpdateStatusRefusesMergeAndMerged covers that
// directly) — this only proves the route surfaces whatever the service
// decides.
func TestUpdateReviewStatusReturnsTheServiceResult(t *testing.T) {
	routes := ReviewRoutes{service: stubReviewService{review: model.Review{ReviewID: "review-1", Status: model.ReviewStatusReady}}}
	rec := patchReviewStatus(t, routes, `{"status":"READY","buildId":"build-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "READY") {
		t.Fatalf("body = %q, want the updated review", rec.Body.String())
	}
}

// TestAdvanceMergeQueueDispatchesThePromotedReview: promoting a review to
// MERGE is what starts its merge gate, and it has to be the review
// AdvanceMergeQueue actually promoted, not necessarily the one any given
// caller had in mind.
func TestAdvanceMergeQueueDispatchesThePromotedReview(t *testing.T) {
	promoted := model.Review{ReviewID: "review-2", TargetBranch: "main", Status: model.ReviewStatusMerge}
	dispatcher := &recordingDispatcher{}
	routes := ReviewRoutes{service: stubReviewService{review: promoted}, dispatcher: dispatcher}

	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance", bytes.NewBufferString(`{"targetBranch":"main"}`))
	rec := httptest.NewRecorder()
	routes.advanceMergeQueue(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(dispatcher.reviews) != 1 || dispatcher.reviews[0] != promoted {
		t.Fatalf("dispatched %+v, want exactly the promoted review %+v", dispatcher.reviews, promoted)
	}
}

// TestAdvanceMergeQueueWithoutADispatcherStillPromotes: an unwired merge
// executor must not stop the promotion itself from being recorded.
func TestAdvanceMergeQueueWithoutADispatcherStillPromotes(t *testing.T) {
	routes := ReviewRoutes{service: stubReviewService{review: model.Review{ReviewID: "review-1", Status: model.ReviewStatusMerge}}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance", bytes.NewBufferString(`{"targetBranch":"main"}`))
	rec := httptest.NewRecorder()
	routes.advanceMergeQueue(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// TestListReviewsTranslatesQueryParamsIntoAReviewFilter is the discovery
// surface the issue calls one filter wide: every new query param must reach
// the repository filter, composed with the existing targetBranch.
func TestListReviewsTranslatesQueryParamsIntoAReviewFilter(t *testing.T) {
	reviews := &stubReviewRepository{}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews?targetBranch=main&sourceBranch=feature/x&status=OPEN&authorUserId=user-1&reviewerUserId=user-2", nil)
	rec := httptest.NewRecorder()

	routes.listReviews(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	want := apirepository.ReviewFilter{
		TargetBranch:   "main",
		SourceBranch:   "feature/x",
		Status:         model.ReviewStatusOpen,
		AuthorUserID:   "user-1",
		ReviewerUserID: "user-2",
	}
	if reviews.gotFilter != want {
		t.Fatalf("filter = %+v, want %+v", reviews.gotFilter, want)
	}
}

func TestAddReviewerCreatesUnderThePathReviewID(t *testing.T) {
	reviewers := &stubReviewReviewerRepository{}
	routes := ReviewRoutes{reviewers: reviewers}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/review-1/reviewers", bytes.NewBufferString(`{"userId":"user-1"}`))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.addReviewer(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	want := model.ReviewReviewer{ReviewID: "review-1", UserID: "user-1"}
	if reviewers.created != want {
		t.Fatalf("created reviewer = %+v, want %+v", reviewers.created, want)
	}
}

func TestListReviewersFiltersByPathReviewID(t *testing.T) {
	reviewers := &stubReviewReviewerRepository{reviewers: []model.ReviewReviewer{{ReviewID: "review-1", UserID: "user-1"}}}
	routes := ReviewRoutes{reviewers: reviewers}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/review-1/reviewers", nil)
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.listReviewers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user-1") {
		t.Fatalf("body = %q, want it to carry the listed reviewer", rec.Body.String())
	}
}

func TestRemoveReviewerReturnsNoContent(t *testing.T) {
	reviewers := &stubReviewReviewerRepository{}
	routes := ReviewRoutes{reviewers: reviewers}
	req := httptest.NewRequest(http.MethodDelete, "/v1/reviews/review-1/reviewers/user-1", nil)
	req.SetPathValue("review_id", "review-1")
	req.SetPathValue("user_id", "user-1")
	rec := httptest.NewRecorder()

	routes.removeReviewer(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if reviewers.deletedReviewID != "review-1" || reviewers.deletedUserID != "user-1" {
		t.Fatalf("deleted (review=%q, user=%q), want (review-1, user-1)", reviewers.deletedReviewID, reviewers.deletedUserID)
	}
}

func TestRemoveReviewerNotFoundReturns404(t *testing.T) {
	reviewers := &stubReviewReviewerRepository{err: apirepository.ErrNotFound}
	routes := ReviewRoutes{reviewers: reviewers}
	req := httptest.NewRequest(http.MethodDelete, "/v1/reviews/review-1/reviewers/user-1", nil)
	req.SetPathValue("review_id", "review-1")
	req.SetPathValue("user_id", "user-1")
	rec := httptest.NewRecorder()

	routes.removeReviewer(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
