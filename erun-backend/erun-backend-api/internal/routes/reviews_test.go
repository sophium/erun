package routes

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
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

type stubBuilds struct {
	build model.Build
	err   error
}

func (b stubBuilds) Get(context.Context, string) (model.Build, error) { return b.build, b.err }

func (b stubBuilds) List(context.Context, apirepository.BuildFilter) ([]model.Build, error) {
	return nil, nil
}

type recordingTrigger struct {
	requests []service.ReleaseRequest
	err      error
}

func (t *recordingTrigger) TriggerRelease(_ context.Context, request service.ReleaseRequest) error {
	t.requests = append(t.requests, request)
	return t.err
}

func patchReviewStatus(t *testing.T, routes ReviewRoutes, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/v1/reviews/review-1/status", bytes.NewBufferString(body))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()
	routes.updateReviewStatus(rec, req)
	return rec
}

func mergedReview() model.Review {
	return model.Review{
		ReviewID:          "review-1",
		TargetBranch:      "main",
		Status:            model.ReviewStatusMerged,
		LastMergedBuildID: "build-1",
	}
}

// TestMergingAReviewTriggersItsRelease is the trigger half of the pipeline: an
// accepted review is what earns a version, and the commit released is the one the
// review actually merged on.
func TestMergingAReviewTriggersItsRelease(t *testing.T) {
	trigger := &recordingTrigger{}
	routes := ReviewRoutes{
		service: stubReviewService{review: mergedReview()},
		builds:  stubBuilds{build: model.Build{BuildID: "build-1", CommitID: "commit-a"}},
		trigger: trigger,
	}
	rec := patchReviewStatus(t, routes, `{"status":"MERGED","buildId":"build-1"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(trigger.requests) != 1 {
		t.Fatalf("triggered %d releases, want one", len(trigger.requests))
	}
	want := service.ReleaseRequest{ReviewID: "review-1", TargetBranch: "main", CommitID: "commit-a"}
	if trigger.requests[0] != want {
		t.Fatalf("release request = %+v, want %+v", trigger.requests[0], want)
	}
}

// TestANonMergedTransitionTriggersNothing: the queue releases what has already
// been accepted; every other transition is not an acceptance.
func TestANonMergedTransitionTriggersNothing(t *testing.T) {
	for _, status := range []model.ReviewStatus{model.ReviewStatusReady, model.ReviewStatusFailed, model.ReviewStatusClosed} {
		trigger := &recordingTrigger{}
		review := mergedReview()
		review.Status = status
		routes := ReviewRoutes{
			service: stubReviewService{review: review},
			builds:  stubBuilds{build: model.Build{BuildID: "build-1", CommitID: "commit-a"}},
			trigger: trigger,
		}
		if rec := patchReviewStatus(t, routes, `{"status":"`+string(status)+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("status %s: HTTP %d, want 200", status, rec.Code)
		}
		if len(trigger.requests) != 0 {
			t.Fatalf("status %s triggered a release: %+v", status, trigger.requests)
		}
	}
}

// TestMergingWithoutAReleaseQueueStillMerges keeps the pre-queue behaviour: a
// control plane with no release queue records reviews exactly as before.
func TestMergingWithoutAReleaseQueueStillMerges(t *testing.T) {
	routes := ReviewRoutes{service: stubReviewService{review: mergedReview()}}
	if rec := patchReviewStatus(t, routes, `{"status":"MERGED","buildId":"build-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestAFailedTriggerSaysTheReviewAlreadyMerged: the transition is persisted by
// the time the trigger runs, so the error must not imply it was rolled back, and
// it must name the recovery.
func TestAFailedTriggerSaysTheReviewAlreadyMerged(t *testing.T) {
	routes := ReviewRoutes{
		service: stubReviewService{review: mergedReview()},
		builds:  stubBuilds{build: model.Build{BuildID: "build-1", CommitID: "commit-a"}},
		trigger: &recordingTrigger{err: errors.New("the queue is unreachable")},
	}
	rec := patchReviewStatus(t, routes, `{"status":"MERGED","buildId":"build-1"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"the review is merged", "POST /v1/releases"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want it to carry %q", body, want)
		}
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
