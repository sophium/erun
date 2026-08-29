package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// reviewAPIStubServer runs a minimal, stateful erun-backend-api double
// covering every route `erun review` drives, so real-run scenarios exercise
// erun-common/platform_client_reviews.go's request/response handling —
// including a real create -> list -> show -> comment -> close round trip —
// rather than only its --dry-run trace branch.
func reviewAPIStubServer(t testing.TB) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	var (
		mu          sync.Mutex
		reviews     = map[string]map[string]any{}
		reviewOrder []string
		comments    = map[string][]map[string]any{}
		nextReview  = 1
		nextComment = 1
		nextBuild   = 1
	)

	mux.HandleFunc("GET /v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenantId": "tenant-1", "userId": "user-1", "username": "test-user", "issuer": "https://idp.example", "subject": "sub-1",
		})
	})

	mux.HandleFunc("POST /v1/reviews", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		for _, existing := range reviews {
			if existing["name"] == body["name"] {
				http.Error(w, "conflict: a review named "+body["name"]+" already exists", http.StatusConflict)
				return
			}
		}
		id := "review-" + strconv.Itoa(nextReview)
		nextReview++
		review := map[string]any{
			"reviewId": id, "tenantId": "tenant-1", "authorUserId": "user-1",
			"name": body["name"], "targetBranch": body["targetBranch"], "sourceBranch": body["sourceBranch"],
			"status": "OPEN", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		}
		reviews[id] = review
		reviewOrder = append(reviewOrder, id)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(review)
	})

	mux.HandleFunc("GET /v1/reviews", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		query := r.URL.Query()
		out := []map[string]any{}
		for _, id := range reviewOrder {
			review := reviews[id]
			if v := query.Get("targetBranch"); v != "" && review["targetBranch"] != v {
				continue
			}
			if v := query.Get("sourceBranch"); v != "" && review["sourceBranch"] != v {
				continue
			}
			if v := query.Get("status"); v != "" && review["status"] != v {
				continue
			}
			if v := query.Get("authorUserId"); v != "" && review["authorUserId"] != v {
				continue
			}
			out = append(out, review)
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("GET /v1/reviews/{review_id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		review, ok := reviews[r.PathValue("review_id")]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(review)
	})

	mux.HandleFunc("PATCH /v1/reviews/{review_id}/status", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		review, ok := reviews[r.PathValue("review_id")]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		review["status"] = body["status"]
		_ = json.NewEncoder(w).Encode(review)
	})

	mux.HandleFunc("GET /v1/reviews/merge-queue", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		targetBranch := r.URL.Query().Get("targetBranch")
		out := []map[string]any{}
		for _, id := range reviewOrder {
			review := reviews[id]
			if review["targetBranch"] != targetBranch {
				continue
			}
			if review["status"] != "READY" && review["status"] != "MERGE" {
				continue
			}
			out = append(out, review)
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("POST /v1/reviews/merge-queue/advance", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		for _, id := range reviewOrder {
			review := reviews[id]
			if review["targetBranch"] != body["targetBranch"] {
				continue
			}
			if review["status"] == "READY" || review["status"] == "MERGE" {
				review["status"] = "MERGED"
				_ = json.NewEncoder(w).Encode(review)
				return
			}
		}
		http.Error(w, "empty queue", http.StatusConflict)
	})

	// override-advance is the CLI plumbing's own concern here (the real
	// unresolved-thread gate and its bypass are covered by
	// erun-backend-api's service tests); this stub only proves the wire
	// round trip and that a blank reason is refused.
	mux.HandleFunc("POST /v1/reviews/merge-queue/override-advance", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.TrimSpace(body["reason"]) == "" {
			http.Error(w, "invalid input", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, id := range reviewOrder {
			review := reviews[id]
			if review["targetBranch"] != body["targetBranch"] {
				continue
			}
			if review["status"] == "READY" || review["status"] == "MERGE" {
				review["status"] = "MERGED"
				_ = json.NewEncoder(w).Encode(review)
				return
			}
		}
		http.Error(w, "empty queue", http.StatusConflict)
	})

	mux.HandleFunc("GET /v1/reviews/{review_id}/comments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(comments[r.PathValue("review_id")])
	})

	mux.HandleFunc("PATCH /v1/reviews/{review_id}/comments/{comment_id}/status", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		for _, comment := range comments[r.PathValue("review_id")] {
			if comment["commentId"] != r.PathValue("comment_id") {
				continue
			}
			if parent, ok := comment["parentCommentId"].(string); ok && parent != "" {
				http.Error(w, "only the root comment of a thread can have its status updated", http.StatusBadRequest)
				return
			}
			comment["status"] = body["status"]
			_ = json.NewEncoder(w).Encode(comment)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	})

	mux.HandleFunc("POST /v1/reviews/{review_id}/comments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		reviewID := r.PathValue("review_id")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		if _, ok := reviews[reviewID]; !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		id := "comment-" + strconv.Itoa(nextComment)
		nextComment++
		line, _ := body["line"].(float64)
		comment := map[string]any{
			"commentId": id, "tenantId": "tenant-1", "reviewId": reviewID, "creatorUserId": "user-1",
			"status": "OPEN", "commitId": body["commitId"], "filePath": body["filePath"], "line": int(line),
			"body": body["body"], "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		}
		if parent, ok := body["parentCommentId"].(string); ok && parent != "" {
			comment["parentCommentId"] = parent
		}
		comments[reviewID] = append(comments[reviewID], comment)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(comment)
	})

	mux.HandleFunc("GET /v1/reviews/{review_id}/builds", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})

	// POST /builds mirrors the real backend's auto-transition: recording a
	// build moves the review straight to READY or FAILED, with no separate
	// PATCH /status call — see erun-docs/docs/collaboration/builds.md.
	mux.HandleFunc("POST /v1/reviews/{review_id}/builds", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		reviewID := r.PathValue("review_id")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		review, ok := reviews[reviewID]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		id := "build-" + strconv.Itoa(nextBuild)
		nextBuild++
		successful, _ := body["successful"].(bool)
		build := map[string]any{
			"buildId": id, "tenantId": "tenant-1", "reviewId": reviewID,
			"successful": successful, "commitId": body["commitId"], "version": body["version"],
			"createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
		}
		if detail, ok := body["failureDetail"].(string); ok && detail != "" {
			build["failureDetail"] = detail
		}
		if successful {
			review["status"] = "READY"
		} else {
			review["status"] = "FAILED"
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(build)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// createReviewJSON runs `review create --output json` against the stub
// server and decodes the resulting reviewId, for scenarios that need a real
// review to act on rather than a --dry-run trace.
func createReviewJSON(t testing.TB, setup env.Setup, name, sourceBranch, targetBranch string) struct{ ReviewID string } {
	t.Helper()
	result := erun.Run(t, []string{
		"review", "create", "--name", name, "--source-branch", sourceBranch, "--target-branch", targetBranch, "--output", "json",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("review create exit %d: %s", result.ExitCode, result.Combined)
	}
	var decoded struct{ ReviewID string }
	if err := json.Unmarshal([]byte(result.Stdout), &decoded); err != nil {
		t.Fatalf("decode review create --output json: %v\n%s", err, result.Stdout)
	}
	if decoded.ReviewID == "" {
		t.Fatalf("expected a non-empty reviewId, got:\n%s", result.Stdout)
	}
	return decoded
}

// postCommentJSON runs `review comment --output json` against the stub
// server and decodes the resulting commentId. parentCommentID is passed as
// --reply-to when non-empty, making the posted comment a reply.
func postCommentJSON(t testing.TB, setup env.Setup, reviewID, commitID, filePath string, line int, body, parentCommentID string) string {
	t.Helper()
	args := []string{
		"review", "comment", reviewID, "--commit", commitID, "--file", filePath, "--line", strconv.Itoa(line), "--output", "json",
	}
	if parentCommentID != "" {
		args = append(args, "--reply-to", parentCommentID)
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: body + "\n"})
	if result.ExitCode != 0 {
		t.Fatalf("review comment exit %d: %s", result.ExitCode, result.Combined)
	}
	var decoded struct{ CommentID string }
	if err := json.Unmarshal([]byte(result.Stdout), &decoded); err != nil {
		t.Fatalf("decode review comment --output json: %v\n%s", err, result.Stdout)
	}
	if decoded.CommentID == "" {
		t.Fatalf("expected a non-empty commentId, got:\n%s", result.Stdout)
	}
	return decoded.CommentID
}

func TestReview(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"review", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/help", normalize.Apply(result.Combined))
	})

	t.Run("list_no_alias_configured", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"review", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with no erun alias configured, got:\n%s", result.Combined)
		}
		golden.Equal(t, "review/list_no_alias_configured", normalize.Apply(result.Combined))
	})

	t.Run("list_dry_run_traces_resolved_call", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"review", "list", "--target-branch", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/list_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("list_mine_dry_run_traces_whoami_and_placeholder_filter", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"review", "list", "--mine", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/list_mine_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("list_rejects_mine_combined_with_author_user_id", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"review", "list", "--mine", "--author-user-id", "user-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit combining --mine with --author-user-id, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "review/list_rejects_mine_combined_with_author_user_id", normalize.Apply(result.Combined))
	})

	t.Run("create_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"review", "create", "--name", "Add widget", "--source-branch", "feature/widget", "--target-branch", "main", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/create_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("full_lifecycle_real_run", func(t *testing.T) {
		// create -> list -> show -> comment -> reply -> close -> list again,
		// against the real stub server, covering every review/comment
		// PlatformClient method's request/response handling in one pass.
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)

		create := erun.Run(t, []string{
			"review", "create", "--name", "Add widget", "--source-branch", "feature/widget", "--target-branch", "main", "--output", "json",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if create.ExitCode != 0 {
			t.Fatalf("create exit %d: %s", create.ExitCode, create.Combined)
		}
		var created struct {
			ReviewID string `json:"reviewId"`
		}
		if err := json.Unmarshal([]byte(create.Stdout), &created); err != nil {
			t.Fatalf("decode create --output json: %v\n%s", err, create.Stdout)
		}
		if created.ReviewID == "" {
			t.Fatalf("expected a non-empty reviewId, got:\n%s", create.Stdout)
		}

		list := erun.Run(t, []string{"review", "list", "--source-branch", "feature/widget"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if list.ExitCode != 0 || !strings.Contains(list.Combined, "Add widget") {
			t.Fatalf("list exit %d: %s", list.ExitCode, list.Combined)
		}

		commentArgs := []string{"review", "comment", created.ReviewID, "--commit", "abc123", "--file", "main.go", "--line", "42"}
		comment := erun.Run(t, commentArgs, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "nit: rename this\n"})
		if comment.ExitCode != 0 {
			t.Fatalf("comment exit %d: %s", comment.ExitCode, comment.Combined)
		}
		if !strings.Contains(comment.Combined, "main.go:42 nit: rename this") {
			t.Fatalf("expected the posted comment's file, line, and body in output, got:\n%s", comment.Combined)
		}

		show := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if show.ExitCode != 0 || !strings.Contains(show.Combined, "nit: rename this") {
			t.Fatalf("show exit %d: %s", show.ExitCode, show.Combined)
		}
		if !strings.Contains(show.Combined, "comments: 1") {
			t.Fatalf("expected show to report one comment, got:\n%s", show.Combined)
		}

		closed := erun.Run(t, []string{"review", "close", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if closed.ExitCode != 0 || !strings.Contains(closed.Combined, "status=CLOSED") {
			t.Fatalf("close exit %d: %s", closed.ExitCode, closed.Combined)
		}

		afterClose := erun.Run(t, []string{"review", "list", "--source-branch", "feature/widget", "--status", "OPEN"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if afterClose.ExitCode != 0 || !strings.Contains(afterClose.Combined, "no reviews") {
			t.Fatalf("expected no OPEN reviews after close, got:\n%s", afterClose.Combined)
		}
	})

	t.Run("create_name_conflict_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		args := []string{"review", "create", "--name", "dup", "--source-branch", "a", "--target-branch", "main"}
		first := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if first.ExitCode != 0 {
			t.Fatalf("first create exit %d: %s", first.ExitCode, first.Combined)
		}
		second := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if second.ExitCode == 0 {
			t.Fatalf("expected the second, colliding create to fail, got 0:\n%s", second.Combined)
		}
		if !strings.Contains(second.Combined, "conflict") {
			t.Fatalf("expected a conflict error, got:\n%s", second.Combined)
		}
	})

	t.Run("comment_dry_run_traces_resolved_call", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"review", "comment", "review-1", "--commit", "abc123", "--file", "main.go", "--line", "42", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "nit: rename this\n"})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/comment_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("comment_reply_dry_run_traces_reply_to", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"review", "comment", "review-1", "--commit", "abc123", "--file", "main.go", "--line", "42", "--reply-to", "comment-1", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env(), Stdin: "good catch, fixed\n"})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/comment_reply_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("resolve_dry_run_traces_resolved_call", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"review", "resolve", "review-1", "comment-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/resolve_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("unresolve_dry_run_traces_resolved_call", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"review", "unresolve", "review-1", "comment-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/unresolve_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("resolve_and_unresolve_root_real_run", func(t *testing.T) {
		// create -> comment (root) -> resolve -> show (unresolved: 0, status=CLOSED)
		// -> unresolve -> show (unresolved: 1, status=OPEN), covering
		// PlatformClient.UpdateCommentStatus's request/response handling and
		// ReviewDetail.UnresolvedThreads end to end.
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)

		created := createReviewJSON(t, setup, "Resolve test", "feature/resolve", "main")
		rootID := postCommentJSON(t, setup, created.ReviewID, "abc123", "main.go", 1, "root note", "")

		resolve := erun.Run(t, []string{"review", "resolve", created.ReviewID, rootID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if resolve.ExitCode != 0 || !strings.Contains(resolve.Combined, "status=CLOSED") {
			t.Fatalf("resolve exit %d: %s", resolve.ExitCode, resolve.Combined)
		}

		afterResolve := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if afterResolve.ExitCode != 0 || !strings.Contains(afterResolve.Combined, "unresolved threads: 0") {
			t.Fatalf("expected zero unresolved threads after resolve, got:\n%s", afterResolve.Combined)
		}
		if !strings.Contains(afterResolve.Combined, "status=CLOSED") {
			t.Fatalf("expected the root comment's status=CLOSED in show output, got:\n%s", afterResolve.Combined)
		}

		unresolve := erun.Run(t, []string{"review", "unresolve", created.ReviewID, rootID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if unresolve.ExitCode != 0 || !strings.Contains(unresolve.Combined, "status=OPEN") {
			t.Fatalf("unresolve exit %d: %s", unresolve.ExitCode, unresolve.Combined)
		}

		afterUnresolve := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if afterUnresolve.ExitCode != 0 || !strings.Contains(afterUnresolve.Combined, "unresolved threads: 1") {
			t.Fatalf("expected one unresolved thread after unresolve, got:\n%s", afterUnresolve.Combined)
		}
	})

	t.Run("resolve_reply_refused_names_root_real_run", func(t *testing.T) {
		// A status change addressed to a reply must be refused, naming the
		// thread's root comment id so the caller can retry against it.
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)

		created := createReviewJSON(t, setup, "Reply refusal test", "feature/reply-refusal", "main")
		rootID := postCommentJSON(t, setup, created.ReviewID, "abc123", "main.go", 1, "root note", "")
		replyID := postCommentJSON(t, setup, created.ReviewID, "abc123", "main.go", 1, "reply note", rootID)

		result := erun.Run(t, []string{"review", "resolve", created.ReviewID, replyID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected resolving a reply to fail, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, rootID) {
			t.Fatalf("expected the refusal to name root comment %s, got:\n%s", rootID, result.Combined)
		}
	})

	t.Run("show_not_found_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		result := erun.Run(t, []string{"review", "show", "missing"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing review, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "not found") {
			t.Fatalf("expected a not-found error, got:\n%s", result.Combined)
		}
	})

	t.Run("record_build_dry_run_traces_resolved_call", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{
			"review", "record-build", "review-1",
			"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3", "--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/record_build_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("record_build_failed_dry_run_traces_failure_detail", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{
			"review", "record-build", "review-1",
			"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
			"--failed", "--failure-detail", "image build failed", "--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/record_build_failed_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("record_build_real_run_moves_review_to_ready", func(t *testing.T) {
		// create -> record-build (successful) -> show (status=READY), covering
		// PlatformClient.CreateBuild's request/response handling and the
		// backend's build-drives-status contract end to end.
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		created := createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

		args := []string{
			"review", "record-build", created.ReviewID,
			"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("record-build exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "successful=true") {
			t.Fatalf("expected the recorded build to report successful=true, got:\n%s", result.Combined)
		}

		show := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if show.ExitCode != 0 || !strings.Contains(show.Combined, "status=READY") {
			t.Fatalf("expected the review to be READY after a successful build, got exit %d:\n%s", show.ExitCode, show.Combined)
		}
	})

	t.Run("record_build_failed_real_run_moves_review_to_failed", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		created := createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

		args := []string{
			"review", "record-build", created.ReviewID,
			"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
			"--failed", "--failure-detail", "image build failed",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("record-build exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "successful=false") {
			t.Fatalf("expected the recorded build to report successful=false, got:\n%s", result.Combined)
		}

		show := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if show.ExitCode != 0 || !strings.Contains(show.Combined, "status=FAILED") {
			t.Fatalf("expected the review to be FAILED after a failed build, got exit %d:\n%s", show.ExitCode, show.Combined)
		}
	})

	t.Run("merge_queue_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"review", "queue", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/merge_queue_help", normalize.Apply(result.Combined))
	})

	t.Run("merge_queue_list_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"review", "queue", "list", "--target-branch", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/merge_queue_list_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("merge_queue_advance_empty_queue_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		result := erun.Run(t, []string{"review", "queue", "advance", "--target-branch", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an empty merge queue, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "empty queue") {
			t.Fatalf("expected the stub's empty-queue error to surface, got:\n%s", result.Combined)
		}
	})

	t.Run("merge_queue_override_advance_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"review", "queue", "override-advance", "--target-branch", "main", "--reason", "hotfix, reviewers unavailable", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/merge_queue_override_advance_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("merge_queue_override_advance_requires_reason", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"review", "queue", "override-advance", "--target-branch", "main", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a blank --reason, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "a reason is required") {
			t.Fatalf("expected the missing-reason error, got:\n%s", result.Combined)
		}
	})

	// merge_queue_override_advance_empty_queue_real_run mirrors
	// merge_queue_advance_empty_queue_real_run: the stub server has no CLI-only
	// path to READY a review (that requires a build result, which this
	// double does not model), so this proves the override's real request/
	// response round trip — reason included — reaches the server rather than
	// only exercising --dry-run's trace branch.
	t.Run("merge_queue_override_advance_empty_queue_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		result := erun.Run(t, []string{
			"review", "queue", "override-advance", "--target-branch", "main", "--reason", "hotfix, reviewers unavailable",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an empty merge queue, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "empty queue") {
			t.Fatalf("expected the stub's empty-queue error to surface, got:\n%s", result.Combined)
		}
	})

	t.Run("output_json", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		args := []string{"review", "create", "--name", "json test", "--source-branch", "a", "--target-branch", "main", "--output", "json"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, `"name": "json test"`) {
			t.Fatalf("expected structured JSON result on stdout, got:\n%s", result.Combined)
		}
	})
}
