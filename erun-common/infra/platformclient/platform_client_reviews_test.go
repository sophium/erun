package platformclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

func TestPlatformClientAdvanceMergeQueueKeepsTheThreadRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"unresolved_threads","message":"review review-1 has 2 unresolved comment thread(s)","reviewId":"review-1","unresolvedThreads":2}`))
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.AdvanceMergeQueue(context.Background(), model.PlatformMergeQueueParams{TargetBranch: "main"})

	var blocked *PlatformMergeQueueBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("AdvanceMergeQueue error = %v, want *PlatformMergeQueueBlockedError", err)
	}
	if blocked.ReviewID != "review-1" || blocked.UnresolvedThreads != 2 {
		t.Fatalf("blocked = %+v, want review-1 with 2 unresolved threads", blocked)
	}
}
