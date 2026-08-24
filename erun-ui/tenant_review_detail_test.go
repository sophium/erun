package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// reviewDetailFixedResponses serves every path reviewDetailAPI answers with a
// fixed body regardless of method; the one path with method-dependent
// behavior (creating a comment) is handled separately in reviewDetailAPI.
var reviewDetailFixedResponses = map[string]string{
	"/v1/whoami":                  `{"tenantId":"tenant-1","userId":"user-1","username":"reader","capabilities":null}`,
	"/v1/reviews/review-1":        `{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}`,
	"/v1/reviews/review-1/builds": `[{"buildId":"build-1","tenantId":"tenant-1","reviewId":"review-1","successful":true,"commitId":"abc123","version":"1.2.3"}]`,
	"/v1/reviews/merge-queue":     `[{"reviewId":"review-0","targetBranch":"main"},{"reviewId":"review-1","targetBranch":"main"}]`,
}

const reviewDetailCommentsPath = "/v1/reviews/review-1/comments"

const reviewDetailThreadJSON = `[
	{"commentId":"comment-1","reviewId":"review-1","creatorUserId":"user-2","status":"OPEN","commitId":"abc123","filePath":"main.go","line":42,"body":"nit: rename this","createdAt":"2026-01-01T00:00:00Z"},
	{"commentId":"comment-2","reviewId":"review-1","creatorUserId":"user-1","status":"OPEN","parentCommentId":"comment-1","commitId":"abc123","filePath":"main.go","line":42,"body":"good catch","createdAt":"2026-01-01T00:05:00Z"}
]`

const reviewDetailNewReplyJSON = `{"commentId":"comment-3","reviewId":"review-1","creatorUserId":"user-1","status":"OPEN","parentCommentId":"comment-1","commitId":"abc123","filePath":"main.go","line":42,"body":"fixed, thanks","createdAt":"2026-01-01T00:10:00Z"}`

// reviewDetailAPI serves one review's detail reads plus a comment-create
// write, refusing every path in forbidden.
func reviewDetailAPI(t *testing.T, forbidden map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if forbidden[req.URL.Path] {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == reviewDetailCommentsPath {
			if req.Method == http.MethodPost {
				_, _ = w.Write([]byte(reviewDetailNewReplyJSON))
			} else {
				_, _ = w.Write([]byte(reviewDetailThreadJSON))
			}
			return
		}
		if body, ok := reviewDetailFixedResponses[req.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, req)
	}))
}

func loadReviewDetailFrom(t *testing.T, app *App, apiURL string) uiReviewDetail {
	t.Helper()
	detail, err := app.LoadReviewDetail(uiReviewDetailInput{
		Tenant: "frs", APIURL: apiURL, CloudProviderAlias: "team-cloud", ReviewID: "review-1",
	})
	if err != nil {
		t.Fatalf("LoadReviewDetail failed: %v", err)
	}
	return detail
}

func TestLoadReviewDetailPopulatesTheReviewItself(t *testing.T) {
	server := reviewDetailAPI(t, nil)
	defer server.Close()

	detail := loadReviewDetailFrom(t, tenantDashboardApp(t), server.URL)

	if detail.APIError != "" || detail.Restricted != "" || detail.Error != "" {
		t.Fatalf("unexpected top-level failure: %+v", detail)
	}
	if detail.Review == nil || detail.Review.Name != "Review 1" {
		t.Fatalf("expected the review itself to be populated, got %+v", detail.Review)
	}
	if !detail.CanComment {
		t.Fatalf("expected an unknown capability set to leave commenting attemptable")
	}
}

func TestLoadReviewDetailPopulatesCommentsBuildsAndQueuePosition(t *testing.T) {
	server := reviewDetailAPI(t, nil)
	defer server.Close()

	detail := loadReviewDetailFrom(t, tenantDashboardApp(t), server.URL)

	if len(detail.Comments) != 2 || detail.Comments[1].ParentCommentID != "comment-1" {
		t.Fatalf("expected the thread's root and reply, got %+v", detail.Comments)
	}
	if len(detail.Builds) != 1 || !detail.Builds[0].Successful {
		t.Fatalf("expected the review's recorded build, got %+v", detail.Builds)
	}
	// review-1 is the second entry the merge-queue stub returns.
	if detail.QueuePosition != 2 {
		t.Fatalf("expected queue position 2, got %d", detail.QueuePosition)
	}
}

func TestLoadReviewDetailDegradesPerSubReadWithoutBlankingTheRest(t *testing.T) {
	server := reviewDetailAPI(t, map[string]bool{"/v1/reviews/review-1/comments": true})
	defer server.Close()

	detail := loadReviewDetailFrom(t, tenantDashboardApp(t), server.URL)

	if detail.Review == nil {
		t.Fatalf("expected the review to still load: %+v", detail)
	}
	if len(detail.Builds) != 1 {
		t.Fatalf("expected builds to still load: %+v", detail.Builds)
	}
	if detail.CommentsError == "" || len(detail.Comments) != 0 {
		t.Fatalf("expected the comments read to fail on its own, got %+v", detail)
	}
}

func TestCreateReviewReplyCopiesTheParentThreadsAnchor(t *testing.T) {
	server := reviewDetailAPI(t, nil)
	defer server.Close()

	comment, err := tenantDashboardApp(t).CreateReviewReply(uiCreateReviewReplyInput{
		Tenant: "frs", APIURL: server.URL, CloudProviderAlias: "team-cloud",
		ReviewID: "review-1", ParentCommentID: "comment-1",
		CommitID: "abc123", FilePath: "main.go", Line: 42,
		Body: "fixed, thanks",
	})
	if err != nil {
		t.Fatalf("CreateReviewReply failed: %v", err)
	}
	if comment.CommentID != "comment-3" || comment.ParentCommentID != "comment-1" || comment.Body != "fixed, thanks" {
		t.Fatalf("unexpected reply: %+v", comment)
	}
}

func TestCreateReviewReplyRequiresABody(t *testing.T) {
	server := reviewDetailAPI(t, nil)
	defer server.Close()

	_, err := tenantDashboardApp(t).CreateReviewReply(uiCreateReviewReplyInput{
		Tenant: "frs", APIURL: server.URL, CloudProviderAlias: "team-cloud",
		ReviewID: "review-1", ParentCommentID: "comment-1", Body: "   ",
	})
	if err == nil || !strings.Contains(err.Error(), "reply body is required") {
		t.Fatalf("expected a body-required error, got %v", err)
	}
}

// TestFetchReviewBuildsConcurrentlyReturnsPartialResultsOnFailure locks the
// concurrency fix's behavior: a failing review's builds read must not lose
// the builds already fetched for the reviews that succeeded, matching the
// old serial loop's break-with-partial-results contract.
func TestFetchReviewBuildsConcurrentlyReturnsPartialResultsOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/reviews/review-bad/builds" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"buildId":"build-good","reviewId":"review-good","successful":true,"commitId":"abc","version":"1.0.0"}]`)
	}))
	defer server.Close()

	client := eruncommon.NewPlatformClient(server.URL, func() (string, error) { return "token", nil })
	reviews := []eruncommon.PlatformReview{{ReviewID: "review-good", Name: "Good"}, {ReviewID: "review-bad", Name: "Bad"}}
	builds, err := fetchReviewBuildsConcurrently(t.Context(), client, reviews)
	if err == nil {
		t.Fatalf("expected the forced failure to surface")
	}
	found := false
	for _, build := range builds {
		if build.ReviewID == "review-good" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the succeeding review's builds to survive the other review's failure, got %+v", builds)
	}
}
