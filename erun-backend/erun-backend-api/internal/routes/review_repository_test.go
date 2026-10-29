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
)

func TestListMergeQueuePassesTheRepositoryFilter(t *testing.T) {
	reviews := &stubReviewRepository{}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/merge-queue?repository=https%3A%2F%2Fgithub.com%2Fsophium%2Ferun&targetBranch=main", nil)
	rec := httptest.NewRecorder()

	routes.listMergeQueue(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if reviews.gotMergeQueueRepository != "https://github.com/sophium/erun" || reviews.gotMergeQueueTargetBranch != "main" {
		t.Fatalf("ListMergeQueue got repository=%q targetBranch=%q, want the query's own values",
			reviews.gotMergeQueueRepository, reviews.gotMergeQueueTargetBranch)
	}
}

func TestListReviewsPassesTheRepositoryFilter(t *testing.T) {
	reviews := &stubReviewRepository{}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews?repository=https%3A%2F%2Fgithub.com%2Fsophium%2Ferun", nil)
	rec := httptest.NewRecorder()

	routes.listReviews(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if reviews.gotFilter.Repository != "https://github.com/sophium/erun" {
		t.Fatalf("filter.Repository = %q, want the query's own value", reviews.gotFilter.Repository)
	}
}

func TestAdvanceMergeQueuePassesTheRepository(t *testing.T) {
	svc := &stubReviewService{review: model.Review{ReviewID: "review-1", Status: model.ReviewStatusMerge}}
	routes := ReviewRoutes{service: svc}
	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance",
		bytes.NewBufferString(`{"repository":"https://github.com/sophium/erun","targetBranch":"main"}`))
	rec := httptest.NewRecorder()

	routes.advanceMergeQueue(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if svc.advanceRepository != "https://github.com/sophium/erun" || svc.advanceTargetBranch != "main" {
		t.Fatalf("AdvanceMergeQueue got repository=%q targetBranch=%q, want the body's own values",
			svc.advanceRepository, svc.advanceTargetBranch)
	}
}

// repositoryFilteringReviewRepository returns only rows the filter names, so an unforwarded filter shows up.
type repositoryFilteringReviewRepository struct {
	rows      []model.Review
	gotFilter apirepository.ReviewFilter
	calls     int
}

func (s *repositoryFilteringReviewRepository) Create(context.Context, model.Review) (model.Review, error) {
	return model.Review{}, nil
}

func (s *repositoryFilteringReviewRepository) Get(_ context.Context, reviewID string) (model.Review, error) {
	for _, review := range s.rows {
		if review.ReviewID == reviewID {
			return review, nil
		}
	}
	return model.Review{}, nil
}

func (s *repositoryFilteringReviewRepository) List(_ context.Context, filter apirepository.ReviewFilter) ([]model.Review, error) {
	s.gotFilter = filter
	s.calls++
	var matched []model.Review
	for _, review := range s.rows {
		if filter.Repository != "" && review.Repository != filter.Repository {
			continue
		}
		if filter.Status != "" && review.Status != filter.Status {
			continue
		}
		matched = append(matched, review)
	}
	return matched, nil
}

func (s *repositoryFilteringReviewRepository) ListMergeQueue(_ context.Context, repository, targetBranch string) ([]model.Review, error) {
	var matched []model.Review
	for _, review := range s.rows {
		if repository != "" && review.Repository != repository {
			continue
		}
		if targetBranch != "" && review.TargetBranch != targetBranch {
			continue
		}
		matched = append(matched, review)
	}
	return matched, nil
}

// Rows are stored canonical, so every remote spelling of one repository must find all of them.
func TestListReviewsFindsEverySpellingOfOneRepository(t *testing.T) {
	reviews := &repositoryFilteringReviewRepository{rows: []model.Review{
		{ReviewID: "review-1", Repository: "https://github.com/sophium/erun", Status: model.ReviewStatusOpen},
		{ReviewID: "review-2", Repository: "https://github.com/sophium/erun", Status: model.ReviewStatusMerged},
		{ReviewID: "review-3", Repository: "https://github.com/sophium/other", Status: model.ReviewStatusOpen},
	}}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews?repository=git%40github.com%3Asophium%2Ferun.git&status=OPEN", nil)
	rec := httptest.NewRecorder()

	routes.listReviews(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	listed := reviewIssueFields(t, rec)
	if len(listed) != 1 || listed[0]["reviewId"] != "review-1" {
		t.Fatalf("listing = %v, want this repository's OPEN review and only it", listed)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/reviews?repository=git%40github.com%3Asophium%2Ferun.git&status=READY", nil)
	rec = httptest.NewRecorder()
	routes.listReviews(rec, req)
	if listed := reviewIssueFields(t, rec); len(listed) != 0 {
		t.Fatalf("listing = %v, want none: this repository has no READY review", listed)
	}
}

// An owner/repo shorthand names no repository and is refused before the query.
func TestListReviewsRefusesARepositoryThatNamesNoRepository(t *testing.T) {
	reviews := &repositoryFilteringReviewRepository{rows: []model.Review{
		{ReviewID: "review-1", Repository: "https://github.com/sophium/erun", Status: model.ReviewStatusOpen},
	}}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews?repository=sophium%2Ferun", nil)
	rec := httptest.NewRecorder()

	routes.listReviews(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v", err)
	}
	if body.Code != "INVALID_REPOSITORY" {
		t.Fatalf("code = %q, want %q", body.Code, "INVALID_REPOSITORY")
	}
	if !strings.Contains(body.Message, "git remote get-url origin") {
		t.Fatalf("message %q does not name the form the caller should pass", body.Message)
	}
	if reviews.calls != 0 {
		t.Fatalf("the refused filter reached the repository anyway (%d queries)", reviews.calls)
	}
}

func TestListMergeQueueResolvesTheRepositorySpelling(t *testing.T) {
	reviews := &repositoryFilteringReviewRepository{rows: []model.Review{
		{ReviewID: "review-1", Repository: "https://github.com/sophium/erun", TargetBranch: "main"},
	}}
	routes := ReviewRoutes{reviews: reviews}

	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/merge-queue?repository=git%40github.com%3Asophium%2Ferun.git&targetBranch=main", nil)
	rec := httptest.NewRecorder()
	routes.listMergeQueue(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if listed := reviewIssueFields(t, rec); len(listed) != 1 {
		t.Fatalf("listing = %v, want this repository's queued review", listed)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/reviews/merge-queue?repository=sophium%2Ferun&targetBranch=main", nil)
	rec = httptest.NewRecorder()
	routes.listMergeQueue(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a repository that names none: %s", rec.Code, rec.Body.String())
	}
}

func TestAdvanceMergeQueueResolvesTheRepositorySpelling(t *testing.T) {
	svc := &stubReviewService{review: model.Review{ReviewID: "review-1", Status: model.ReviewStatusMerge}}
	routes := ReviewRoutes{service: svc}

	req := httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance",
		bytes.NewBufferString(`{"repository":"git@github.com:sophium/erun.git","targetBranch":"main"}`))
	rec := httptest.NewRecorder()
	routes.advanceMergeQueue(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if svc.advanceRepository != "https://github.com/sophium/erun" {
		t.Fatalf("AdvanceMergeQueue got repository=%q, want the canonical identity", svc.advanceRepository)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/reviews/merge-queue/advance",
		bytes.NewBufferString(`{"repository":"sophium/erun","targetBranch":"main"}`))
	rec = httptest.NewRecorder()
	routes.advanceMergeQueue(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a repository that names none: %s", rec.Code, rec.Body.String())
	}
	if svc.advanceRepository != "https://github.com/sophium/erun" {
		t.Fatalf("the refused body reached the service as %q", svc.advanceRepository)
	}
}
