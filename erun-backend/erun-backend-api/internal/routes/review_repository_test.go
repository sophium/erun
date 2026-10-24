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

// TestListMergeQueuePassesTheRepositoryFilter: two repositories a tenant
// serves both have a main, so the repository is half of what names a queue.
// A query parameter dropped on the way through would answer the union of
// both, which is the defect the filter exists to remove.
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

// TestListReviewsPassesTheRepositoryFilter pins the same half for discovery:
// --repository narrows a listing to one repository rather than being accepted
// and ignored.
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

// TestAdvanceMergeQueuePassesTheRepository: the body's repository is what
// settles which of several same-branch queues is advanced, so it has to reach
// the service rather than be read and discarded.
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

// repositoryFilteringReviewRepository answers a listing the way the SQL does:
// a row is in the answer only when the filter names it, so a test can tell a
// filter that reached the query from one that was answered for.
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

// The reported failure: `erun review list --repository <spelling>` answered a
// silent subset of one repository's reviews, because the filter was compared
// verbatim against a repository column whose rows were canonicalized when they
// were written. Every row of one repository belongs to the answer whichever
// remote spelling the caller holds, and a repository that genuinely has none
// still answers none.
func TestListReviewsFindsEverySpellingOfOneRepository(t *testing.T) {
	reviews := &repositoryFilteringReviewRepository{rows: []model.Review{
		// As an SSH checkout records them: the review stores the canonical
		// identity, never the spelling the caller happened to hold.
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
	// The other branch of the same pair: a repository with nothing in the
	// asked-for state answers nothing rather than being widened.
	req = httptest.NewRequest(http.MethodGet, "/v1/reviews?repository=git%40github.com%3Asophium%2Ferun.git&status=READY", nil)
	rec = httptest.NewRecorder()
	routes.listReviews(rec, req)
	if listed := reviewIssueFields(t, rec); len(listed) != 0 {
		t.Fatalf("listing = %v, want none: this repository has no READY review", listed)
	}
}

// The other half of the reported failure: `--repository owner/repo` names no
// repository at all, and answering it with the one row a caller had once
// recorded under that same shorthand is a silent subset of the repository they
// meant. It is refused before the query, like a mistyped `--status`.
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

// A queue is addressed the same way a listing is, and for the same reason: an
// environment holding the SSH remote must reach the queue its HTTPS remote
// recorded, and a value that names no repository must not address a subset of
// one.
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

// The advance takes the repository in its body, so it carries the same
// canonicalization: advancing `git@github.com:o/r.git` must address the queue
// that remote's HTTPS form recorded rather than answering EMPTY_QUEUE for it.
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
