package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

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

// An unknown status must be refused before the query, naming the accepted values.
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
