package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// reviewWriteAPI serves the four write routes CreateReview/CloseReview/
// AdvanceMergeQueue/CreateReviewComment drive, refusing every path in
// forbidden the same way reviewDetailAPI/tenantDashboardAPI do. Routes are
// keyed on "METHOD /path" and dispatched through a map rather than a
// method+path switch, so adding a route never grows one function's branching.
func reviewWriteAPI(t *testing.T, forbidden map[string]bool) *httptest.Server {
	t.Helper()
	routes := map[string]func(w http.ResponseWriter, req *http.Request){
		"POST /v1/reviews": func(w http.ResponseWriter, req *http.Request) {
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), `"sourceBranch":"feature/1348-x"`) {
				t.Fatalf("expected the request to carry the pushed source branch, got %s", body)
			}
			_, _ = w.Write([]byte(`{"reviewId":"review-1","tenantId":"tenant-1","name":"Open the review","targetBranch":"main","sourceBranch":"feature/1348-x","status":"OPEN"}`))
		},
		"PATCH /v1/reviews/review-1/status": func(w http.ResponseWriter, req *http.Request) {
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), `"status":"CLOSED"`) {
				t.Fatalf("expected the request to close the review, got %s", body)
			}
			_, _ = w.Write([]byte(`{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"CLOSED"}`))
		},
		"POST /v1/reviews/merge-queue/advance":          reviewWriteAdvanceHandler(t),
		"POST /v1/reviews/merge-queue/override-advance": reviewWriteOverrideAdvanceHandler(t),
		"POST /v1/reviews/review-1/comments": func(w http.ResponseWriter, req *http.Request) {
			body, _ := io.ReadAll(req.Body)
			if strings.Contains(string(body), "parentCommentId") {
				t.Fatalf("expected a new top-level thread to carry no parentCommentId, got %s", body)
			}
			if !strings.Contains(string(body), `"filePath":"main.go"`) || !strings.Contains(string(body), `"line":10`) {
				t.Fatalf("expected the request to carry the diff-line anchor, got %s", body)
			}
			_, _ = w.Write([]byte(`{"commentId":"comment-1","reviewId":"review-1","creatorUserId":"user-1","status":"OPEN","commitId":"abc123","filePath":"main.go","line":10,"body":"why this line?","createdAt":"2026-01-01T00:00:00Z"}`))
		},
	}
	return newReviewWriteServer(routes, forbidden)
}

// reviewWriteAdvanceHandler promotes the queue head to MERGED, except for
// the magic "blocked" target branch a test uses to exercise the
// unresolved-thread refusal without standing up a second server.
func reviewWriteAdvanceHandler(t *testing.T) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if strings.Contains(string(body), `"targetBranch":"blocked"`) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"unresolved_threads","message":"review review-2 has 3 unresolved comment thread(s)","reviewId":"review-2","unresolvedThreads":3}`))
			return
		}
		if !strings.Contains(string(body), `"targetBranch":"main"`) {
			t.Fatalf("expected the request to carry the target branch, got %s", body)
		}
		_, _ = w.Write([]byte(`{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"MERGED"}`))
	}
}

// reviewWriteOverrideAdvanceHandler promotes the head regardless of status,
// requiring only that the reason travelled with the request.
func reviewWriteOverrideAdvanceHandler(t *testing.T) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(body), `"reason":"hotfix, reviewers unavailable"`) {
			t.Fatalf("expected the request to carry the reason, got %s", body)
		}
		_, _ = w.Write([]byte(`{"reviewId":"review-2","tenantId":"tenant-1","name":"Review 2","targetBranch":"blocked","sourceBranch":"feature","status":"MERGE"}`))
	}
}

func newReviewWriteServer(
	routes map[string]func(w http.ResponseWriter, req *http.Request),
	forbidden map[string]bool,
) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		key := req.Method + " " + req.URL.Path
		if forbidden[key] {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		route, ok := routes[key]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		route(w, req)
	}))
}

func TestCreateReviewOpensAReviewFromThePushedBranch(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	review, err := tenantDashboardApp(t, server.URL).CreateReview(uiCreateReviewInput{
		Tenant: "frs",
		Name:   "Open the review", TargetBranch: "main", SourceBranch: "feature/1348-x",
	})
	if err != nil {
		t.Fatalf("CreateReview failed: %v", err)
	}
	if review.ReviewID != "review-1" || review.Status != "OPEN" {
		t.Fatalf("unexpected review: %+v", review)
	}
}

func TestCreateReviewRequiresNameAndBothBranches(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).CreateReview(uiCreateReviewInput{
		Tenant: "frs",
		Name:   "", TargetBranch: "main", SourceBranch: "feature/1348-x",
	})
	if err == nil || !strings.Contains(err.Error(), "are required") {
		t.Fatalf("expected a required-fields error, got %v", err)
	}
}

func TestCreateReviewSurfacesForbiddenAsAnError(t *testing.T) {
	server := reviewWriteAPI(t, map[string]bool{"POST /v1/reviews": true})
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).CreateReview(uiCreateReviewInput{
		Tenant: "frs",
		Name:   "Open the review", TargetBranch: "main", SourceBranch: "feature/1348-x",
	})
	if err == nil {
		t.Fatalf("expected the refused write to surface as an error")
	}
}

func TestCloseReviewTransitionsToClosed(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	review, err := tenantDashboardApp(t, server.URL).CloseReview(uiCloseReviewInput{
		Tenant: "frs", ReviewID: "review-1",
	})
	if err != nil {
		t.Fatalf("CloseReview failed: %v", err)
	}
	if review.Status != "CLOSED" {
		t.Fatalf("expected the review to close, got %+v", review)
	}
}

func TestCloseReviewSurfacesForbiddenAsAnError(t *testing.T) {
	server := reviewWriteAPI(t, map[string]bool{"PATCH /v1/reviews/review-1/status": true})
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).CloseReview(uiCloseReviewInput{
		Tenant: "frs", ReviewID: "review-1",
	})
	if err == nil {
		t.Fatalf("expected the refused write to surface as an error")
	}
}

func TestAdvanceMergeQueueAdvancesTheTargetBranchsHead(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	review, err := tenantDashboardApp(t, server.URL).AdvanceMergeQueue(uiAdvanceMergeQueueInput{
		Tenant: "frs", TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("AdvanceMergeQueue failed: %v", err)
	}
	if review.Blocked {
		t.Fatalf("expected an unblocked promotion, got %+v", review)
	}
	if review.Status != "MERGED" {
		t.Fatalf("expected the queue head to advance to merged, got %+v", review)
	}
}

func TestAdvanceMergeQueueRequiresATargetBranch(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).AdvanceMergeQueue(uiAdvanceMergeQueueInput{
		Tenant: "frs",
	})
	if err == nil || !strings.Contains(err.Error(), "target branch is required") {
		t.Fatalf("expected a target-branch-required error, got %v", err)
	}
}

// TestAdvanceMergeQueueReportsUnresolvedThreadsAsABlockNotAnError is the UX
// contract: a refusal that cannot be acted on (a bare error string) is the
// dead end AGENTS.md's "Smooth, Seamless, No Dead Ends" refuses to accept.
// The caller needs the count and the review id to route the operator to the
// threads, so this surfaces as a successful, structured "blocked" result.
func TestAdvanceMergeQueueReportsUnresolvedThreadsAsABlockNotAnError(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	review, err := tenantDashboardApp(t, server.URL).AdvanceMergeQueue(uiAdvanceMergeQueueInput{
		Tenant: "frs", TargetBranch: "blocked",
	})
	if err != nil {
		t.Fatalf("AdvanceMergeQueue: %v, want a blocked result rather than an error", err)
	}
	if !review.Blocked || review.ReviewID != "review-2" || review.UnresolvedThreads == nil || *review.UnresolvedThreads != 3 {
		t.Fatalf("result = %+v, want Blocked=true ReviewID=review-2 UnresolvedThreads=3", review)
	}
}

func TestOverrideAdvanceMergeQueueBypassesTheGate(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	review, err := tenantDashboardApp(t, server.URL).OverrideAdvanceMergeQueue(uiOverrideAdvanceMergeQueueInput{
		Tenant: "frs", TargetBranch: "blocked", Reason: "hotfix, reviewers unavailable",
	})
	if err != nil {
		t.Fatalf("OverrideAdvanceMergeQueue failed: %v", err)
	}
	if review.ReviewID != "review-2" || review.Status != "MERGE" {
		t.Fatalf("expected the blocked review to promote via override, got %+v", review)
	}
}

func TestOverrideAdvanceMergeQueueRequiresAReason(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).OverrideAdvanceMergeQueue(uiOverrideAdvanceMergeQueueInput{
		Tenant: "frs", TargetBranch: "blocked",
	})
	if err == nil || !strings.Contains(err.Error(), "a reason is required") {
		t.Fatalf("expected a reason-required error, got %v", err)
	}
}

func TestCreateReviewCommentStartsATopLevelThreadWithNoParent(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	comment, err := tenantDashboardApp(t, server.URL).CreateReviewComment(uiCreateReviewCommentInput{
		Tenant:   "frs",
		ReviewID: "review-1", CommitID: "abc123", FilePath: "main.go", Line: 10,
		Body: "why this line?",
	})
	if err != nil {
		t.Fatalf("CreateReviewComment failed: %v", err)
	}
	if comment.ParentCommentID != "" {
		t.Fatalf("expected a new top-level thread with no parent, got %+v", comment)
	}
	if comment.FilePath != "main.go" || comment.Line != 10 {
		t.Fatalf("expected the diff-line anchor to round-trip, got %+v", comment)
	}
}

func TestCreateReviewCommentRequiresADiffLineAnchor(t *testing.T) {
	server := reviewWriteAPI(t, nil)
	defer server.Close()

	_, err := tenantDashboardApp(t, server.URL).CreateReviewComment(uiCreateReviewCommentInput{
		Tenant:   "frs",
		ReviewID: "review-1", Body: "why this line?",
	})
	if err == nil || !strings.Contains(err.Error(), "diff line anchor") {
		t.Fatalf("expected a diff-line-anchor-required error, got %v", err)
	}
}

// TestTenantDashboardReportsWriteCapabilitiesAlongsideReads pins that a caller
// missing the create-review or advance-merge-queue capability sees that named
// up front, the same CanComment already gives the reply composer, instead of
// discovering it only when the write 403s.
func TestTenantDashboardReportsWriteCapabilitiesAlongsideReads(t *testing.T) {
	var requests []string
	capabilities := `[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/reviews"},{"method":"GET","path":"/v1/reviews/merge-queue"},{"method":"GET","path":"/v1/reviews/{review_id}/builds"},{"method":"GET","path":"/v1/audit-events"},{"method":"POST","path":"/v1/reviews"}]`
	server := tenantDashboardAPI(t, capabilities, nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if !dashboard.CanCreateReview {
		t.Fatalf("expected CanCreateReview true when POST /v1/reviews is granted, got %+v", dashboard)
	}
	if dashboard.CanAdvanceMergeQueue {
		t.Fatalf("expected CanAdvanceMergeQueue false when the write is not granted, got %+v", dashboard)
	}
	if dashboard.CanOverrideMergeQueue {
		t.Fatalf("expected CanOverrideMergeQueue false when the write is not granted, got %+v", dashboard)
	}
}

// TestTenantDashboardReportsOverrideMergeQueueCapabilitySeparately pins that
// the override is gated on its own route, distinct from CanAdvanceMergeQueue
// — a tenant can grant ordinary advance without granting the override.
func TestTenantDashboardReportsOverrideMergeQueueCapabilitySeparately(t *testing.T) {
	var requests []string
	capabilities := `[{"method":"GET","path":"/v1/whoami"},{"method":"GET","path":"/v1/reviews"},{"method":"GET","path":"/v1/reviews/merge-queue"},{"method":"GET","path":"/v1/reviews/{review_id}/builds"},{"method":"GET","path":"/v1/audit-events"},{"method":"POST","path":"/v1/reviews/merge-queue/advance"},{"method":"POST","path":"/v1/reviews/merge-queue/override-advance"}]`
	server := tenantDashboardAPI(t, capabilities, nil, &requests)
	defer server.Close()

	dashboard := loadTenantDashboardFrom(t, tenantDashboardApp(t, server.URL))

	if !dashboard.CanAdvanceMergeQueue {
		t.Fatalf("expected CanAdvanceMergeQueue true when the write is granted, got %+v", dashboard)
	}
	if !dashboard.CanOverrideMergeQueue {
		t.Fatalf("expected CanOverrideMergeQueue true when the write is granted, got %+v", dashboard)
	}
}

func TestReviewDetailReportsCanCloseAlongsideCanComment(t *testing.T) {
	server := reviewDetailAPI(t, nil)
	defer server.Close()

	detail := loadReviewDetailFrom(t, tenantDashboardApp(t, server.URL))

	if !detail.CanClose {
		t.Fatalf("expected an unknown capability set to leave closing attemptable, got %+v", detail)
	}
}
