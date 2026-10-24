package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// reviewIssueFields decodes a review-returning route's body into the raw field
// maps, so a test can tell "the field is absent from the wire" apart from "the
// field is present and empty". For a review's issue link those are different
// answers: absent is a review the platform has no issue for, and a client that
// reads an empty string as a link would route an operator to an issue named by
// nothing.
func reviewIssueFields(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var reviews []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &reviews); err != nil {
		t.Fatalf("body %q is not a JSON array of reviews: %v", rec.Body.String(), err)
	}
	return reviews
}

// TestListReviewsMarksAnIssueDerivedFromTheSourceBranchAsInferred is the
// reproduction of the state this route could not answer. A review created
// without an issue recorded is linked by the branch it proposes, and the link
// travels with its provenance, because an inferred link presented as a
// declared one claims something the platform does not know.
//
// Before this, a listing carried no issue reference at all for a branch that
// names one, so every review read as unlinked and a board pivoting on the
// issue had nothing to pivot on.
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

// TestListReviewsLeavesABranchOutsideTheConventionUnlinked: the derivation is
// best-effort, so a branch that does not follow the documented convention is
// unlinked rather than guessed at. A branch merely containing a number is not
// a link to it.
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

// TestGetReviewCarriesTheDerivedIssueRefToo: one review is the same resource
// the listing returns, so it answers the same question the same way. A single
// review reached by id is how a client checks one link, and a shape that
// varied between the two surfaces would make the derived field look like
// something only a listing produces.
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

// TestListMergeQueueCarriesTheDerivedIssueRef: the queue is a listing of the
// same resource, and it is the surface that answers what state an issue's
// review has reached. A review carrying its issue link in one listing and not
// in the other is the inconsistency that makes a client join on nothing.
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
