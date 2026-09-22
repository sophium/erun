package routes

import (
	"bytes"
	"context"
	"encoding/json"
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
	// overrideReason/overrideTargetBranch record OverrideAdvanceMergeQueue's
	// call so a test can assert the request body actually reached the service.
	overrideReason       string
	overrideTargetBranch string
}

func (s *stubReviewService) PrepareCreate(review model.Review) model.Review { return review }

func (s *stubReviewService) AdvanceMergeQueue(context.Context, string) (model.Review, error) {
	return s.review, s.err
}

func (s *stubReviewService) OverrideAdvanceMergeQueue(_ context.Context, targetBranch, reason string) (model.Review, error) {
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

// TestUpdateReviewStatusReturnsTheServiceResult: PATCH .../status is a thin
// adapter over ReviewService.UpdateStatus, which is where MERGE/MERGED are
// refused (service.TestUpdateStatusRefusesMergeAndMerged covers that
// directly) — this only proves the route surfaces whatever the service
// decides.
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

// TestAdvanceMergeQueuePromotesTheReview: the route surfaces exactly the
// review AdvanceMergeQueue promoted.
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

// TestAdvanceMergeQueueBlockedReportsCountAndReview: a refusal that names
// nothing an operator can act on is the dead end AGENTS.md's "Smooth,
// Seamless, No Dead Ends" section refuses to accept — the body must carry the
// unresolved count and the review id so a caller can route the operator
// straight to the threads.
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

// TestAdvanceMergeQueueOccupiedNamesTheBlocker: an occupied MERGE slot is a
// conflict with the branch's current state, not a missing resource, and the
// body has to carry the review holding it — that review is the operator's next
// move (finish it, or requeue it back to READY).
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

// TestUpdateReviewStatusRequeueRefusalNamesTheStatus: requeue recovers only a
// review at MERGE, so a refusal from any other status has to name the status
// the review is actually in rather than reporting it as missing.
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

// TestOverrideAdvanceMergeQueuePassesTargetBranchAndReason: the route is a
// thin adapter over the service, so its only job is getting both request
// fields to OverrideAdvanceMergeQueue unchanged.
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

// TestOverrideAdvanceMergeQueueRefusalReportsError: a refused override (blank
// reason, no audit logger configured, ...) must report the failure, not a
// promotion that never happened.
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

// TestListReviewsRefusesAnUnknownStatusFilter: the read route handed any
// `?status=` value straight to the repository, so an unrecognised one matched
// no row and answered 200 with an empty list -- a mistyped filter was
// indistinguishable from a review queue that genuinely had nothing in that
// state, which is the conclusion an operator acts on. The refusal must name
// the accepted values, and it must happen before the query, or the caller
// still receives the empty listing this route is being fixed to stop
// producing.
func TestListReviewsRefusesAnUnknownStatusFilter(t *testing.T) {
	reviews := &stubReviewRepository{}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews?status=bogus-not-a-status", nil)
	rec := httptest.NewRecorder()

	routes.listReviews(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v", err)
	}
	if body.Code != "INVALID_QUERY" {
		t.Fatalf("code = %q, want %q", body.Code, "INVALID_QUERY")
	}
	for _, want := range []string{"OPEN", "CLOSED", "FAILED", "READY", "MERGE", "MERGED"} {
		if !strings.Contains(body.Message, want) {
			t.Fatalf("message %q does not name the accepted value %s", body.Message, want)
		}
	}
	if reviews.gotFilter.Status != "" {
		t.Fatalf("the refused filter reached the repository as %q, want no query at all", reviews.gotFilter.Status)
	}
}

// Case-insensitivity is the documented behavior on this route (erun#2015), so
// a lower-case spelling resolves to the stored one rather than being refused
// alongside the values that are genuinely outside the vocabulary.
func TestListReviewsResolvesALowerCaseStatusFilter(t *testing.T) {
	reviews := &stubReviewRepository{}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews?status=ready", nil)
	rec := httptest.NewRecorder()

	routes.listReviews(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if reviews.gotFilter.Status != model.ReviewStatusReady {
		t.Fatalf("filter status = %q, want %q", reviews.gotFilter.Status, model.ReviewStatusReady)
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

// TestGenericRepositoryErrorCarriesAStatusDerivedCode is the base case the
// envelope exists for: a caller branching on `code` must never see it absent,
// even on a route with no business-specific code of its own.
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

// TestAdvanceMergeQueueEmptyQueueReportsEmptyQueueCode: the queue-empty case
// must be distinguishable from every other 404 this route can return, so a
// caller can tell "nothing to promote" from "another review is merging".
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

// TestAdvanceMergeQueueInvalidTargetBranchReportsItsCode: an empty
// targetBranch must not be confused with OverrideAdvanceMergeQueue's own
// ErrInvalidInput (a blank reason) — both are 400 but only one is this code.
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

// TestUpdateReviewStatusInvalidTransitionReportsCodeAndDetails: a caller
// asserting MERGE/MERGED directly must get INVALID_TRANSITION with the
// documented details shape (from/to/validTargets), not a bare status text.
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

// TestUpdateReviewStatusMissingBuildIDReportsInvalidBody: READY/FAILED
// without a buildId is a missing required field, not a bare 400.
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

// TestUpdateReviewStatusMalformedJSONReportsInvalidBody: a decode failure is
// the same INVALID_BODY code as a semantically missing field.
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
