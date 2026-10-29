package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// reviewIssueFields decodes raw field maps so an absent field is distinguishable from an empty one.
func reviewIssueFields(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var reviews []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &reviews); err != nil {
		t.Fatalf("body %q is not a JSON array of reviews: %v", rec.Body.String(), err)
	}
	return reviews
}

func TestListReviewsMarksAnIssueDerivedFromTheSourceBranchAsInferred(t *testing.T) {
	reviews := &stubReviewRepository{reviews: []model.Review{{
		ReviewID:     "review-1",
		SourceBranch: "bug/2212-issue-ref-from-branch",
		Status:       model.ReviewStatusOpen,
	}}}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews", nil)
	rec := httptest.NewRecorder()

	routes.listReviews(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	listed := reviewIssueFields(t, rec)
	if len(listed) != 1 {
		t.Fatalf("listing = %v, want one review", listed)
	}
	if listed[0]["issueRef"] != "2212" {
		t.Fatalf("issueRef = %v, want the number the source branch names", listed[0]["issueRef"])
	}
	if listed[0]["issueRefSource"] != "INFERRED" {
		t.Fatalf("issueRefSource = %v, want INFERRED: a reference parsed out of a branch name is a guess, and the caller has to be able to see that", listed[0]["issueRefSource"])
	}
}

// A branch merely containing a number is not a link to it.
func TestListReviewsLeavesABranchOutsideTheConventionUnlinked(t *testing.T) {
	reviews := &stubReviewRepository{reviews: []model.Review{{
		ReviewID:     "review-1",
		SourceBranch: "bugfix/2212nottheconvention",
		Status:       model.ReviewStatusOpen,
	}}}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews", nil)
	rec := httptest.NewRecorder()

	routes.listReviews(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	listed := reviewIssueFields(t, rec)
	if len(listed) != 1 {
		t.Fatalf("listing = %v, want one review", listed)
	}
	if value, ok := listed[0]["issueRef"]; ok {
		t.Fatalf("issueRef = %v, want no issue reference at all for a branch outside the convention", value)
	}
	if value, ok := listed[0]["issueRefSource"]; ok {
		t.Fatalf("issueRefSource = %v, want it omitted alongside an absent issueRef rather than claiming a provenance for nothing", value)
	}
}

func TestGetReviewCarriesTheDerivedIssueRefToo(t *testing.T) {
	reviews := &stubReviewRepository{reviews: []model.Review{{
		ReviewID:     "review-1",
		SourceBranch: "feature/2212-issue-ref-from-branch",
		Status:       model.ReviewStatusOpen,
	}}}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/review-1", nil)
	req.SetPathValue("review_id", "review-1")
	rec := httptest.NewRecorder()

	routes.getReview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not a review object: %v", rec.Body.String(), err)
	}
	if got["issueRef"] != "2212" || got["issueRefSource"] != "INFERRED" {
		t.Fatalf("body = %q, want the branch's issue marked inferred on the single-review surface too", rec.Body.String())
	}
}

func TestListMergeQueueCarriesTheDerivedIssueRef(t *testing.T) {
	reviews := &stubReviewRepository{reviews: []model.Review{{
		ReviewID:     "review-1",
		SourceBranch: "bug/2212-issue-ref-from-branch",
		Status:       model.ReviewStatusReady,
	}}}
	routes := ReviewRoutes{reviews: reviews}
	req := httptest.NewRequest(http.MethodGet, "/v1/reviews/merge-queue?targetBranch=main", nil)
	rec := httptest.NewRecorder()

	routes.listMergeQueue(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	listed := reviewIssueFields(t, rec)
	if len(listed) != 1 {
		t.Fatalf("listing = %v, want one review", listed)
	}
	if listed[0]["issueRef"] != "2212" || listed[0]["issueRefSource"] != "INFERRED" {
		t.Fatalf("listing = %v, want the branch's issue marked inferred", listed)
	}
}
