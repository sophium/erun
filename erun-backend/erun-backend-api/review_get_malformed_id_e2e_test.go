package backendapi

import (
	"net/http"
	"testing"
)

// TestGetReviewMalformedIDReturns400NotFoundForAbsentAndSuccessForValid is
// the regression test for erun#2136: GET /v1/reviews/{id} reported 500 for a
// syntactically malformed id (the review_id column is UUID, and PostgreSQL
// rejects "not-a-uuid" at parse time with SQLSTATE 22P02), reachable through
// this package's real HTTP handler and routing exactly the way a live
// deployment serves it, driven against a real migrated PostgreSQL. It fails
// without the internal/repository/errors.go fix (normalizeNoRows previously
// let that SQLSTATE fall through unclassified, so writeRepositoryError's
// default case reported 500) and passes with it. The well-formed-but-absent
// and valid cases are pinned alongside it so the fix cannot collapse either
// of those two distinct outcomes into the new 400.
func TestGetReviewMalformedIDReturns400NotFoundForAbsentAndSuccessForValid(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	reviewID := e2eOpenReview(t, srv.URL, "malformed-id-e2e", uniqueBranchName(t, "main"), uniqueBranchName(t, "feature"))

	code, body := e2eRequest(t, srv.URL, http.MethodGet, "/v1/reviews/not-a-uuid", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("malformed id: HTTP %d (want 400): %s", code, body)
	}

	code, body = e2eRequest(t, srv.URL, http.MethodGet, "/v1/reviews/01a01b39-0000-7000-8000-000000000000", nil)
	if code != http.StatusNotFound {
		t.Fatalf("well-formed but absent id: HTTP %d (want 404): %s", code, body)
	}

	review := readMergeReview(t, srv.URL, reviewID)
	if review.ReviewID != reviewID {
		t.Fatalf("valid id: got review %q, want %q", review.ReviewID, reviewID)
	}
}
