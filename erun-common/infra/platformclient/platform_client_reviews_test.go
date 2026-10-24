package platformclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPlatformClientAdvanceMergeQueueNamesTheOccupyingReview pins the client
// half of the occupied-slot refusal: the platform reports which review already
// holds the branch, and that has to reach the operator through the typed error
// rather than an opaque response body, the same way an unresolved thread does.
func TestPlatformClientAdvanceMergeQueueNamesTheOccupyingReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"MERGE_QUEUE_OCCUPIED","message":"merge queue for main already has a review at MERGE","details":{"targetBranch":"main","reviewId":"review-9","name":"Land the widget","sourceBranch":"feature/widget"}}`))
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.AdvanceMergeQueue(context.Background(), PlatformMergeQueueParams{TargetBranch: "main"})

	var occupied *PlatformMergeQueueOccupiedError
	if !errors.As(err, &occupied) {
		t.Fatalf("AdvanceMergeQueue error = %v, want *PlatformMergeQueueOccupiedError", err)
	}
	if occupied.TargetBranch != "main" || occupied.ReviewID != "review-9" ||
		occupied.Name != "Land the widget" || occupied.SourceBranch != "feature/widget" {
		t.Fatalf("occupied = %+v, want the review already at MERGE on main", occupied)
	}
	if !errors.Is(err, ErrPlatformConflict) {
		t.Fatalf("err = %v, want it to still unwrap to ErrPlatformConflict", err)
	}
	if !strings.Contains(occupied.Error(), "review-9") || !strings.Contains(occupied.Error(), "requeue") {
		t.Fatalf("Error() = %q, want it to name the review and the requeue remedy", occupied.Error())
	}
}

// TestPlatformClientAdvanceMergeQueueKeepsTheThreadRefusal: the occupied-slot
// decoration sits beside the unresolved-thread one on the same 409, so the
// thread refusal must still reach a caller as its own typed error.
func TestPlatformClientAdvanceMergeQueueKeepsTheThreadRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"unresolved_threads","message":"review review-1 has 2 unresolved comment thread(s)","reviewId":"review-1","unresolvedThreads":2}`))
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.AdvanceMergeQueue(context.Background(), PlatformMergeQueueParams{TargetBranch: "main"})

	var blocked *PlatformMergeQueueBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("AdvanceMergeQueue error = %v, want *PlatformMergeQueueBlockedError", err)
	}
	if blocked.ReviewID != "review-1" || blocked.UnresolvedThreads != 2 {
		t.Fatalf("blocked = %+v, want review-1 with 2 unresolved threads", blocked)
	}
}
