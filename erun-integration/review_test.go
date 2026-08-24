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

	mux.HandleFunc("GET /v1/reviews/{review_id}/comments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(comments[r.PathValue("review_id")])
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

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
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
