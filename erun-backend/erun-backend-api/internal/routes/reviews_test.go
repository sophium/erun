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
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

type stubReviewRepository struct {
	reviews                   []model.Review
	err                       error
	gotFilter                 apirepository.ReviewFilter
	gotCreated                model.Review
	gotMergeQueueRepository   string
	gotMergeQueueTargetBranch string
}

// Create echoes its input like Returning("*"): columns with bun:"-" keep the caller's value.
func (s *stubReviewRepository) Create(_ context.Context, review model.Review) (model.Review, error) {
	s.gotCreated = review
	if s.err != nil {
		return model.Review{}, s.err
	}
	created := review
	created.ReviewID = "review-1"
	return created, nil
}

func (s *stubReviewRepository) Get(context.Context, string) (model.Review, error) {
	if s.err != nil {
		return model.Review{}, s.err
	}
	if len(s.reviews) > 0 {
		return s.reviews[0], nil
	}
	return model.Review{}, nil
}

func (s *stubReviewRepository) List(_ context.Context, filter apirepository.ReviewFilter) ([]model.Review, error) {
	s.gotFilter = filter
	return s.reviews, s.err
}

func (s *stubReviewRepository) ListMergeQueue(_ context.Context, repository, targetBranch string) ([]model.Review, error) {
	s.gotMergeQueueRepository = repository
	s.gotMergeQueueTargetBranch = targetBranch
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
	review               model.Review
	err                  error
	overrideReason       string
	overrideTargetBranch string
	overrideRepository   string
	advanceRepository    string
	advanceTargetBranch  string
	preparedReview       model.Review
	prepareErr           error
}

// PrepareCreate mirrors the real OPEN default.
func (s *stubReviewService) PrepareCreate(review model.Review) (model.Review, error) {
	s.preparedReview = review
	if s.prepareErr != nil {
		return model.Review{}, s.prepareErr
	}
	if review.Status == "" {
		review.Status = model.ReviewStatusOpen
	}
	return review, nil
}

func (s *stubReviewService) AdvanceMergeQueue(_ context.Context, repository, targetBranch string) (model.Review, error) {
	s.advanceRepository = repository
	s.advanceTargetBranch = targetBranch
	return s.review, s.err
}

func (s *stubReviewService) OverrideAdvanceMergeQueue(_ context.Context, repository, targetBranch, reason string) (model.Review, error) {
	s.overrideRepository = repository
	s.overrideTargetBranch = targetBranch
	s.overrideReason = reason
	return s.review, s.err
}

func (s *stubReviewService) UpdateStatus(context.Context, string, model.ReviewStatus, string, string) (model.Review, error) {
	return s.review, s.err
}

func patchReviewStatus(t *testing.T, routes ReviewRoutes, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/v1/reviews/review-1/status", bytes.NewBufferString(body))
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()
	routes.updateReviewStatus(rec, req)
	return rec
}

func TestUpdateReviewStatusReturnsTheServiceResult(t *testing.T) {
	routes := ReviewRoutes{service: &stubReviewService{review: model.Review{ReviewID: "review-1", Status: model.ReviewStatusReady}}}
	rec := patchReviewStatus(t, routes, `{"status":"READY","buildId":"build-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "READY") {
		t.Fatalf("body = %q, want the updated review", rec.Body.String())
	}
}

func TestAdvanceMergeQueuePromotesTheReview(t *testing.T) {
	promoted := model.Review{ReviewID: "review-2", TargetBranch: "main", Status: model.ReviewStatusMerge}
	routes := ReviewRoutes{service: &stubReviewService{review: promoted}}

	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance", bytes.NewBufferString(`{"targetBranch":"main"}`))
	rec := httptest.NewRecorder()
	routes.advanceMergeQueue(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "review-2") {
		t.Fatalf("body = %q, want the promoted review", rec.Body.String())
	}
}

func TestAdvanceMergeQueueBlockedReportsCountAndReview(t *testing.T) {
	routes := ReviewRoutes{service: &stubReviewService{err: &service.UnresolvedThreadsError{ReviewID: "review-1", UnresolvedThreads: 3}}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance", bytes.NewBufferString(`{"targetBranch":"main"}`))
	rec := httptest.NewRecorder()

	routes.advanceMergeQueue(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"unresolvedThreads":3`) || !strings.Contains(body, `"reviewId":"review-1"`) {
		t.Fatalf("body = %q, want the unresolved count and the review id", body)
	}
}

func TestAdvanceMergeQueueOccupiedNamesTheBlocker(t *testing.T) {
	svc := &stubReviewService{err: &service.MergeQueueOccupiedError{
		TargetBranch: "main",
		ReviewID:     "review-9",
		Name:         "Land the widget",
		SourceBranch: "feature/widget",
	}}
	routes := ReviewRoutes{service: svc}

	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance", bytes.NewBufferString(`{"targetBranch":"main"}`))
	rec := httptest.NewRecorder()
	routes.advanceMergeQueue(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"code":"MERGE_QUEUE_OCCUPIED"`, `"reviewId":"review-9"`, `"sourceBranch":"feature/widget"`, `"targetBranch":"main"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want it to carry %s", body, want)
		}
	}
	if !strings.Contains(body, "requeue") {
		t.Fatalf("body = %q, want the message to name the requeue remedy", body)
	}
}

func TestUpdateReviewStatusRequeueRefusalNamesTheStatus(t *testing.T) {
	svc := &stubReviewService{err: &service.ReviewNotMergingError{ReviewID: "review-1", Status: model.ReviewStatusReady}}
	routes := ReviewRoutes{service: svc}

	rec := patchReviewStatus(t, routes, `{"status":"READY"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"REVIEW_NOT_MERGING"`) || !strings.Contains(body, `"reviewId":"review-1"`) || !strings.Contains(body, `"status":"READY"`) {
		t.Fatalf("body = %q, want the code and the review's actual status", body)
	}
}

func TestOverrideAdvanceMergeQueuePassesTargetBranchAndReason(t *testing.T) {
	promoted := model.Review{ReviewID: "review-1", TargetBranch: "main", Status: model.ReviewStatusMerge}
	svc := &stubReviewService{review: promoted}
	routes := ReviewRoutes{service: svc}

	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/override-advance", bytes.NewBufferString(`{"targetBranch":"main","reason":"hotfix"}`))
	rec := httptest.NewRecorder()
	routes.overrideAdvanceMergeQueue(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if svc.overrideTargetBranch != "main" || svc.overrideReason != "hotfix" {
		t.Fatalf("service saw targetBranch=%q reason=%q, want main/hotfix", svc.overrideTargetBranch, svc.overrideReason)
	}
}

func TestOverrideAdvanceMergeQueueRefusalReportsError(t *testing.T) {
	svc := &stubReviewService{err: apirepository.ErrInvalidInput}
	routes := ReviewRoutes{service: svc}

	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/override-advance", bytes.NewBufferString(`{"targetBranch":"main","reason":""}`))
	rec := httptest.NewRecorder()
	routes.overrideAdvanceMergeQueue(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestGenericRepositoryErrorCarriesAStatusDerivedCode(t *testing.T) {
	reviewers := &stubReviewReviewerRepository{err: apirepository.ErrNotFound}
	routes := ReviewRoutes{reviewers: reviewers}
	req := httptest.NewRequest(http.MethodDelete, "/v1/reviews/review-1/reviewers/user-1", nil)
	req.SetPathValue("review_id", "review-1")
	req.SetPathValue("user_id", "user-1")
	rec := httptest.NewRecorder()

	routes.removeReviewer(rec, req)

	if !strings.Contains(rec.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("body = %q, want a NOT_FOUND code", rec.Body.String())
	}
}

func TestAdvanceMergeQueueEmptyQueueReportsEmptyQueueCode(t *testing.T) {
	routes := ReviewRoutes{service: &stubReviewService{err: &service.EmptyMergeQueueError{TargetBranch: "main"}}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance", bytes.NewBufferString(`{"targetBranch":"main"}`))
	rec := httptest.NewRecorder()

	routes.advanceMergeQueue(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"EMPTY_QUEUE"`) {
		t.Fatalf("body = %q, want code EMPTY_QUEUE", rec.Body.String())
	}
}

func TestAdvanceMergeQueueInvalidTargetBranchReportsItsCode(t *testing.T) {
	routes := ReviewRoutes{service: &stubReviewService{err: service.ErrInvalidTargetBranch}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance", bytes.NewBufferString(`{"targetBranch":""}`))
	rec := httptest.NewRecorder()

	routes.advanceMergeQueue(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"INVALID_TARGET_BRANCH"`) {
		t.Fatalf("body = %q, want code INVALID_TARGET_BRANCH", rec.Body.String())
	}
}

func TestUpdateReviewStatusInvalidTransitionReportsCodeAndDetails(t *testing.T) {
	routes := ReviewRoutes{service: &stubReviewService{err: &service.InvalidTransitionError{
		From:         model.ReviewStatusOpen,
		To:           model.ReviewStatusMerged,
		ValidTargets: []model.ReviewStatus{model.ReviewStatusFailed, model.ReviewStatusReady, model.ReviewStatusClosed},
	}}}
	rec := patchReviewStatus(t, routes, `{"status":"MERGED"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"code":"INVALID_TRANSITION"`, `"from":"OPEN"`, `"to":"MERGED"`, `"validTargets":["FAILED","READY","CLOSED"]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want it to contain %q", body, want)
		}
	}
}

func TestUpdateReviewStatusMissingBuildIDReportsInvalidBody(t *testing.T) {
	routes := ReviewRoutes{service: &stubReviewService{err: &service.MissingBuildIDError{Status: model.ReviewStatusFailed}}}
	rec := patchReviewStatus(t, routes, `{"status":"FAILED"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"INVALID_BODY"`) || !strings.Contains(body, `"field":"buildId"`) {
		t.Fatalf("body = %q, want code INVALID_BODY naming field buildId", body)
	}
}

func TestUpdateReviewStatusMalformedJSONReportsInvalidBody(t *testing.T) {
	routes := ReviewRoutes{service: &stubReviewService{}}
	rec := patchReviewStatus(t, routes, `{not json`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"INVALID_BODY"`) {
		t.Fatalf("body = %q, want code INVALID_BODY", rec.Body.String())
	}
}

func TestAdvanceMergeQueueAmbiguousQueueReportsItsCode(t *testing.T) {
	svc := &stubReviewService{err: &service.AmbiguousMergeQueueError{
		TargetBranch: "main",
		Repositories: []string{"https://github.com/sophium/erun", "https://github.com/sophium/other"},
	}}
	routes := ReviewRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance", bytes.NewBufferString(`{"targetBranch":"main"}`))
	rec := httptest.NewRecorder()

	routes.advanceMergeQueue(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"code":"MERGE_QUEUE_AMBIGUOUS"`, `"targetBranch":"main"`, "https://github.com/sophium/other"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want it to carry %s", body, want)
		}
	}
	// The empty array must be present, not omitted.
	if !strings.Contains(body, `"unrecordedRepositoryReviewIds":[]`) {
		t.Fatalf("body = %q, want an empty unrecordedRepositoryReviewIds rather than an absent one", body)
	}
}

func TestAdvanceMergeQueueAmbiguousQueueNamesUnattributableRows(t *testing.T) {
	svc := &stubReviewService{err: &service.AmbiguousMergeQueueError{
		TargetBranch:        "main",
		Repositories:        []string{"https://github.com/sophium/erun", "https://github.com/sophium/other"},
		UnrecordedReviewIDs: []string{"01a0cdc9-1dcc-7eea-9233-3b564acd3849"},
	}}
	routes := ReviewRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance", bytes.NewBufferString(`{"targetBranch":"main"}`))
	rec := httptest.NewRecorder()

	routes.advanceMergeQueue(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"unrecordedRepositoryReviewIds":["01a0cdc9-1dcc-7eea-9233-3b564acd3849"]`,
		"01a0cdc9-1dcc-7eea-9233-3b564acd3849",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want it to carry %s", body, want)
		}
	}
}
