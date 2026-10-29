package backendapi

import (
	"net/http"
	"testing"
)

// TestGetReviewMalformedIDReturns400NotFoundForAbsentAndSuccessForValid: review_id is UUID, so
// PostgreSQL rejects a malformed id with SQLSTATE 22P02, which must map to 400, not 500.
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
