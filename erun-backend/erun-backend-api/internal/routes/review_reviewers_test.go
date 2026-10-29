package routes

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	apirepository "github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

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
