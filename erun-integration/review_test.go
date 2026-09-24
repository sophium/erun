package integration

import (
	"encoding/json"
	"fmt"
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
			if existing["name"] == body["name"] && existing["repository"] == body["repository"] {
				http.Error(w, "conflict: a review named "+body["name"]+" already exists", http.StatusConflict)
				return
			}
		}
		id := "review-" + strconv.Itoa(nextReview)
		nextReview++
		review := map[string]any{
			"reviewId": id, "tenantId": "tenant-1", "authorUserId": "user-1",
			"repository": body["repository"],
			"name":       body["name"], "targetBranch": body["targetBranch"], "sourceBranch": body["sourceBranch"],
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
			if v := query.Get("repository"); v != "" && review["repository"] != v {
				continue
			}
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
		repository := r.URL.Query().Get("repository")
		out := []map[string]any{}
		for _, id := range reviewOrder {
			review := reviews[id]
			if review["targetBranch"] != targetBranch {
				continue
			}
			if repository != "" && review["repository"] != repository {
				continue
			}
			if review["status"] != "READY" && review["status"] != "MERGE" {
				continue
			}
			out = append(out, review)
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	// advance mirrors the real backend's own promotion target (READY -> MERGE,
	// never straight to MERGED) so a real-run scenario can drive a review to
	// MERGE and then exercise `review requeue`'s MERGE -> READY recovery path.
	mux.HandleFunc("POST /v1/reviews/merge-queue/advance", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		// One MERGE per target branch, the same invariant the real backend
		// enforces: a branch whose slot is taken refuses with the review
		// holding it rather than promoting the next one.
		for _, id := range reviewOrder {
			review := reviews[id]
			if review["targetBranch"] != body["targetBranch"] || review["status"] != "MERGE" {
				continue
			}
			if body["repository"] != "" && review["repository"] != body["repository"] {
				continue
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": "MERGE_QUEUE_OCCUPIED",
				"message": fmt.Sprintf("merge queue for %v already has a review at MERGE: %v (%v, %v); complete it or requeue it back to READY before advancing",
					review["targetBranch"], review["reviewId"], review["name"], review["sourceBranch"]),
				"details": map[string]any{
					"targetBranch": review["targetBranch"],
					"reviewId":     review["reviewId"],
					"name":         review["name"],
					"sourceBranch": review["sourceBranch"],
				},
			})
			return
		}
		for _, id := range reviewOrder {
			review := reviews[id]
			if review["targetBranch"] != body["targetBranch"] {
				continue
			}
			if body["repository"] != "" && review["repository"] != body["repository"] {
				continue
			}
			if review["status"] == "READY" {
				review["status"] = "MERGE"
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
			if body["repository"] != "" && review["repository"] != body["repository"] {
				continue
			}
			if review["status"] == "READY" {
				review["status"] = "MERGE"
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

// reviewRepository is the repository the review scenarios below open their
// reviews in. A review has to name one — two repositories a tenant serves
// would otherwise share a queue for the same target branch — and scenarios
// that do not exercise the checkout's own origin derive it, name it here so
// the assertion is about the review rather than about the runner's cwd.
const reviewRepository = "https://github.com/sophium/erun"

// otherRepository is a second repository the same tenant serves, which is the
// shape that made a target branch alone an ambiguous name for a queue.
const otherRepository = "https://github.com/sophium/other"

// createReviewJSON runs `review create --output json` against the stub
// server and decodes the resulting reviewId, for scenarios that need a real
// review to act on rather than a --dry-run trace.
func createReviewJSON(t testing.TB, setup env.Setup, name, sourceBranch, targetBranch string) struct{ ReviewID string } {
	t.Helper()
	result := erun.Run(t, []string{
		"review", "create", "--name", name, "--repository", reviewRepository,
		"--source-branch", sourceBranch, "--target-branch", targetBranch, "--output", "json",
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
	t.Parallel()
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

	// The reported failure: a mistyped --status reached the platform verbatim,
	// came back as a clean empty listing, and printed "no reviews" at exit 0 --
	// indistinguishable from a review queue that genuinely has nothing in that
	// state. Both halves are asserted together below, because the defect is
	// precisely that the two were the same output.
	t.Run("list_mistyped_status_is_refused_rather_than_listed_as_empty", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)

		// The control: a valid status that genuinely matches nothing. This one
		// legitimately reports an empty result at exit 0.
		empty := erun.Run(t, []string{"review", "list", "--status", "OPEN"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if empty.ExitCode != 0 {
			t.Fatalf("a valid status matching nothing should still exit 0, got %d:\n%s", empty.ExitCode, empty.Combined)
		}
		if !strings.Contains(empty.Combined, "no reviews") {
			t.Fatalf("expected the genuine empty listing to say 'no reviews', got:\n%s", empty.Combined)
		}

		// The reproduction: the same empty queue, reached by a typo.
		bogus := erun.Run(t, []string{"review", "list", "--status", "BOGUS"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if bogus.ExitCode == 0 {
			t.Fatalf("a mistyped --status must not exit 0, got:\n%s", bogus.Combined)
		}
		if strings.Contains(bogus.Combined, "no reviews") {
			t.Fatalf("a mistyped --status must not be reported as an empty listing, got:\n%s", bogus.Combined)
		}
		for _, want := range []string{"BOGUS", "OPEN", "CLOSED", "FAILED", "READY", "MERGE", "MERGED"} {
			if !strings.Contains(bogus.Combined, want) {
				t.Fatalf("expected the refusal to name %q, got:\n%s", want, bogus.Combined)
			}
		}
		golden.Equal(t, "review/list_mistyped_status_is_refused", normalize.Apply(bogus.Combined))

		// A near-miss typo is the same refusal: the defect was that any value
		// outside the vocabulary listed as empty, not only an obviously foreign one.
		nearMiss := erun.Run(t, []string{"review", "list", "--status", "MERGEDD"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if nearMiss.ExitCode == 0 || strings.Contains(nearMiss.Combined, "no reviews") {
			t.Fatalf("a near-miss --status must be refused too, got exit %d:\n%s", nearMiss.ExitCode, nearMiss.Combined)
		}
	})

	// Case-insensitivity is the documented behavior here, so a lower-case
	// spelling must resolve to the stored one rather than be refused with it:
	// the rejection is only for values outside the six in any casing.
	t.Run("list_lowercase_status_resolves_to_the_stored_spelling", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

		result := erun.Run(t, []string{"review", "list", "--status", "open"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("a lower-case --status should resolve, not be refused, got %d:\n%s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "Add widget") {
			t.Fatalf("expected --status open to match the OPEN review, got:\n%s", result.Combined)
		}
	})

	// The reported failure: `--repository sophium/erun` names a repository on
	// whichever forge the caller had in mind, so the platform answered it with
	// the subset of rows a caller had once recorded under that same shorthand
	// -- a silently short listing for a repository holding many reviews. It is
	// refused as a bad argument, like a mistyped --status, and equally before
	// the alias lookup: no alias is configured here on purpose.
	t.Run("list_refuses_a_repository_that_names_no_repository", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"review", "list", "--repository", "sophium/erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("a --repository that names no repository must not list, got:\n%s", result.Combined)
		}
		if strings.Contains(result.Combined, "no reviews") {
			t.Fatalf("a --repository that names no repository must not be reported as an empty listing, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "git remote get-url origin") {
			t.Fatalf("expected the refusal to name the form to pass instead, not an alias-resolution failure, got:\n%s", result.Combined)
		}
	})

	// The other half: whichever spelling of the repository's own remote the
	// caller holds reaches the rows the other spelling recorded. The stub
	// matches the filter exactly, so a client that stopped canonicalizing
	// would answer empty here.
	t.Run("list_finds_a_repository_by_any_spelling_of_its_remote", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

		for _, spelling := range []string{"git@github.com:sophium/erun.git", "https://github.com/sophium/erun"} {
			result := erun.Run(t, []string{"review", "list", "--repository", spelling}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
			if result.ExitCode != 0 {
				t.Fatalf("review list --repository %s exit %d:\n%s", spelling, result.ExitCode, result.Combined)
			}
			if !strings.Contains(result.Combined, "Add widget") {
				t.Fatalf("review list --repository %s did not find the review recorded under the same repository's other remote:\n%s", spelling, result.Combined)
			}
		}
	})

	// The refusal is a bad-argument error, so it must not depend on the platform
	// being configured at all: a caller with no alias still learns their filter
	// was wrong rather than being sent to set up an alias that would not help.
	t.Run("list_rejects_unknown_status_before_resolving_an_alias", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"review", "list", "--status", "BOGUS"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a mistyped --status, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "unsupported review status") {
			t.Fatalf("expected the refusal, not an alias-resolution failure, got:\n%s", result.Combined)
		}
	})

	t.Run("create_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"review", "create", "--name", "Add widget", "--repository", reviewRepository,
			"--source-branch", "feature/widget", "--target-branch", "main", "--dry-run"}
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
			"review", "create", "--name", "Add widget", "--repository", reviewRepository,
			"--source-branch", "feature/widget", "--target-branch", "main", "--output", "json",
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
		args := []string{"review", "create", "--name", "dup", "--repository", reviewRepository,
			"--source-branch", "a", "--target-branch", "main"}
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

	t.Run("record_build_gate_dry_run_traces_resolved_call", func(t *testing.T) {
		// --gate reports the merge queue's own GATE build kind and carries no
		// version, since the gate publishes nothing.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{
			"review", "record-build", "review-1",
			"--commit", "abc123def456abc123def456abc123def456abcd", "--gate", "--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/record_build_gate_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("record_build_gate_failed_known_infrastructure_signature_refused", func(t *testing.T) {
		// A failed GATE build whose --failure-detail names one of erun's own
		// known infrastructure signatures (see
		// erun-common/gate_run_failure_classifier.go) is refused locally --
		// before any network call -- rather than recorded FAILED: builds.successful
		// is a plain boolean with no INCONCLUSIVE, so recording it would move the
		// review out of the merge queue for a network/registry blip the sibling
		// gate-run classifier already knows is not a verdict about the change.
		// No alias is seeded: the refusal fires before alias resolution, same as
		// the desktop-coverage preflight beside it.
		setup := env.New(t)
		args := []string{
			"review", "record-build", "review-1",
			"--commit", "abc123def456abc123def456abc123def456abcd", "--gate", "--failed",
			"--failure-detail", "failed to solve: failed to resolve source metadata for ghcr.io/sophium/erun-devops:1.0.246: failed to authorize: failed to fetch oauth token: Post \"https://ghcr.io/token\": net/http: TLS handshake timeout",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected the refusal to exit non-zero, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "known erun infrastructure") || !strings.Contains(result.Combined, "gate-run report") {
			t.Fatalf("expected a refusal naming the known-infrastructure signature and the gate-run report remedy, got:\n%s", result.Combined)
		}
	})

	t.Run("record_build_gate_failed_genuine_failure_not_refused", func(t *testing.T) {
		// A failure-detail that does not match a known infrastructure
		// signature is unaffected: it traces and reports the resolved call
		// exactly as before this classifier existed.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{
			"review", "record-build", "review-1",
			"--commit", "abc123def456abc123def456abc123def456abcd", "--gate", "--failed",
			"--failure-detail", "go test ./...: TestFoo: got 3, want 5",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/record_build_gate_failed_genuine_failure_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("report_merged_dry_run_traces_resolved_call", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{
			"review", "report-merged", "review-1",
			"--build-id", "build-1", "--remote-url", "https://github.com/org/repo.git", "--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/report_merged_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("report_merged_real_run_moves_review_to_merged", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		created := createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

		args := []string{
			"review", "report-merged", created.ReviewID,
			"--build-id", "build-1", "--remote-url", "https://github.com/org/repo.git",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("report-merged exit %d: %s", result.ExitCode, result.Combined)
		}

		show := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if show.ExitCode != 0 || !strings.Contains(show.Combined, "status=MERGED") {
			t.Fatalf("expected the review to be MERGED after report-merged, got exit %d:\n%s", show.ExitCode, show.Combined)
		}
	})

	t.Run("requeue_dry_run_traces_resolved_call", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"review", "requeue", "review-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/requeue_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("requeue_real_run_moves_merge_back_to_ready", func(t *testing.T) {
		// create -> record-build (READY) -> queue advance (MERGE) -> requeue
		// (READY), covering the real MERGE -> READY recovery round trip
		// erun#2241 asked for: a review wedged at MERGE (e.g. by a batched
		// gate-merge whose sibling landed but was never promoted) can be
		// freed without a raw HTTP call.
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		created := createReviewJSON(t, setup, "Add widget", "feature/widget", "main")

		build := erun.Run(t, []string{
			"review", "record-build", created.ReviewID,
			"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if build.ExitCode != 0 {
			t.Fatalf("record-build exit %d: %s", build.ExitCode, build.Combined)
		}

		advance := erun.Run(t, []string{"review", "queue", "advance", "--target-branch", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if advance.ExitCode != 0 || !strings.Contains(advance.Combined, "status=MERGE") {
			t.Fatalf("advance exit %d: %s", advance.ExitCode, advance.Combined)
		}

		requeue := erun.Run(t, []string{"review", "requeue", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if requeue.ExitCode != 0 || !strings.Contains(requeue.Combined, "status=READY") {
			t.Fatalf("requeue exit %d: %s", requeue.ExitCode, requeue.Combined)
		}

		show := erun.Run(t, []string{"review", "show", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if show.ExitCode != 0 || !strings.Contains(show.Combined, "status=READY") {
			t.Fatalf("expected the review to be READY after requeue, got exit %d:\n%s", show.ExitCode, show.Combined)
		}
	})

	t.Run("requeue_refused_from_open", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		created := createReviewJSON(t, setup, "Add widget", "feature/widget-open", "main")

		result := erun.Run(t, []string{"review", "requeue", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit requeuing an OPEN review, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "OPEN") || !strings.Contains(result.Combined, "not MERGE") {
			t.Fatalf("expected the refusal to name the review's actual status OPEN, got:\n%s", result.Combined)
		}
	})

	t.Run("requeue_refused_from_ready", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		created := createReviewJSON(t, setup, "Add widget", "feature/widget-ready", "main")
		build := erun.Run(t, []string{
			"review", "record-build", created.ReviewID,
			"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if build.ExitCode != 0 {
			t.Fatalf("record-build exit %d: %s", build.ExitCode, build.Combined)
		}

		result := erun.Run(t, []string{"review", "requeue", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit requeuing a READY review, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "READY") || !strings.Contains(result.Combined, "not MERGE") {
			t.Fatalf("expected the refusal to name the review's actual status READY, got:\n%s", result.Combined)
		}
	})

	t.Run("requeue_refused_from_merged", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		created := createReviewJSON(t, setup, "Add widget", "feature/widget-merged", "main")

		reportMerged := erun.Run(t, []string{
			"review", "report-merged", created.ReviewID,
			"--build-id", "build-1", "--remote-url", "https://github.com/org/repo.git",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if reportMerged.ExitCode != 0 {
			t.Fatalf("report-merged exit %d: %s", reportMerged.ExitCode, reportMerged.Combined)
		}

		result := erun.Run(t, []string{"review", "requeue", created.ReviewID}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit requeuing a MERGED review, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "MERGED") || !strings.Contains(result.Combined, "not MERGE") {
			t.Fatalf("expected the refusal to name the review's actual status MERGED, got:\n%s", result.Combined)
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
		result := erun.Run(t, []string{"review", "queue", "list", "--repository", reviewRepository, "--target-branch", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/merge_queue_list_dry_run", normalize.Apply(result.Combined))
	})

	// The reported failure, end to end: one tenant, two repositories, both
	// with a `main`. The queue used to be keyed on the target branch alone, so
	// each repository's `erun review queue list --target-branch main` printed
	// the other's waiting work as its own, and advancing promoted whichever
	// review happened to be queued first — into a gate that runs in the
	// checkout of a repository whose branch that review does not name.
	t.Run("merge_queue_is_the_repositorys_own", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)

		readied := func(name, repository, sourceBranch string) string {
			reviewID := createReviewJSON(t, setup, name, sourceBranch, "main")
			build := erun.Run(t, []string{
				"review", "record-build", reviewID.ReviewID,
				"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3", "--output", "json",
			}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
			if build.ExitCode != 0 {
				t.Fatalf("record-build for %s exit %d: %s", name, build.ExitCode, build.Combined)
			}
			return reviewID.ReviewID
		}
		// createReviewJSON always opens in reviewRepository; the second
		// repository's review is opened directly so both branches — and the
		// name — are spelled the same way in each.
		ours := readied("Land the widget", reviewRepository, "feature/widget")
		theirs := erun.Run(t, []string{
			"review", "create", "--name", "Land the widget", "--repository", otherRepository,
			"--source-branch", "feature/widget", "--target-branch", "main", "--output", "json",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if theirs.ExitCode != 0 {
			t.Fatalf("the other repository's create exit %d: %s; the name and branch pair are only reserved within one repository",
				theirs.ExitCode, theirs.Combined)
		}
		var other struct {
			ReviewID string `json:"reviewId"`
		}
		if err := json.Unmarshal([]byte(theirs.Stdout), &other); err != nil {
			t.Fatalf("decode the other repository's review: %v", err)
		}
		build := erun.Run(t, []string{
			"review", "record-build", other.ReviewID,
			"--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3", "--output", "json",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if build.ExitCode != 0 {
			t.Fatalf("record-build for the other repository exit %d: %s", build.ExitCode, build.Combined)
		}

		for _, tc := range []struct {
			repository string
			want       string
			absent     string
		}{
			{reviewRepository, ours, other.ReviewID},
			{otherRepository, other.ReviewID, ours},
		} {
			listed := erun.Run(t, []string{
				"review", "queue", "list", "--repository", tc.repository, "--target-branch", "main",
			}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
			if listed.ExitCode != 0 {
				t.Fatalf("queue list for %s exit %d: %s", tc.repository, listed.ExitCode, listed.Combined)
			}
			if !strings.Contains(listed.Combined, tc.want) {
				t.Fatalf("queue list for %s = %q, want it to name %s", tc.repository, listed.Combined, tc.want)
			}
			if strings.Contains(listed.Combined, tc.absent) {
				t.Fatalf("queue list for %s = %q, want the other repository's %s left out", tc.repository, listed.Combined, tc.absent)
			}
		}

		// Advancing names one repository's queue; the other's review stays
		// where it is rather than being promoted by a queue it is not in.
		promoted := erun.Run(t, []string{
			"review", "queue", "advance", "--repository", otherRepository, "--target-branch", "main", "--output", "json",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if promoted.ExitCode != 0 {
			t.Fatalf("advance exit %d: %s", promoted.ExitCode, promoted.Combined)
		}
		var advanced struct {
			ReviewID string `json:"reviewId"`
			Status   string `json:"status"`
		}
		if err := json.Unmarshal([]byte(promoted.Stdout), &advanced); err != nil {
			t.Fatalf("decode the promoted review: %v", err)
		}
		if advanced.ReviewID != other.ReviewID || advanced.Status != "MERGE" {
			t.Fatalf("promoted %+v, want the named repository's own head %s at MERGE", advanced, other.ReviewID)
		}
	})

	t.Run("merge_queue_advance_empty_queue_real_run", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)
		result := erun.Run(t, []string{"review", "queue", "advance", "--repository", reviewRepository, "--target-branch", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an empty merge queue, got:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "empty queue") {
			t.Fatalf("expected the stub's empty-queue error to surface, got:\n%s", result.Combined)
		}
	})

	// merge_queue_advance_names_the_occupying_review drives the reported
	// failure end to end: a review already at MERGE (its gate build still
	// running) while a second READY review is advanced. The refusal has to
	// reach the operator naming the review holding the slot — which is the one
	// they must finish or requeue — rather than as a not-found that sends them
	// looking for a missing endpoint.
	t.Run("merge_queue_advance_names_the_occupying_review", func(t *testing.T) {
		setup := env.New(t)
		server := reviewAPIStubServer(t)
		platformAlias(t, setup, server)

		occupying := createReviewJSON(t, setup, "Land the widget", "feature/widget", "main")
		for _, args := range [][]string{
			{"review", "record-build", occupying.ReviewID, "--commit", "abc123def456abc123def456abc123def456abcd", "--version", "1.2.3"},
			{"review", "queue", "advance", "--repository", reviewRepository, "--target-branch", "main"},
		} {
			if result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()}); result.ExitCode != 0 {
				t.Fatalf("%v exit %d: %s", args, result.ExitCode, result.Combined)
			}
		}

		queued := createReviewJSON(t, setup, "Add the next widget", "feature/next-widget", "main")
		build := erun.Run(t, []string{
			"review", "record-build", queued.ReviewID,
			"--commit", "def456abc123def456abc123def456abc123def4", "--version", "1.2.4",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if build.ExitCode != 0 {
			t.Fatalf("record-build exit %d: %s", build.ExitCode, build.Combined)
		}

		result := erun.Run(t, []string{"review", "queue", "advance", "--repository", reviewRepository, "--target-branch", "main"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit advancing while %s holds MERGE, got:\n%s", occupying.ReviewID, result.Combined)
		}
		if !strings.Contains(result.Combined, occupying.ReviewID) || !strings.Contains(result.Combined, "feature/widget") {
			t.Fatalf("expected the refusal to name the occupying review %s and its source branch, got:\n%s", occupying.ReviewID, result.Combined)
		}
		if !strings.Contains(result.Combined, "requeue") {
			t.Fatalf("expected the refusal to name the requeue remedy, got:\n%s", result.Combined)
		}
		// The client decodes the refusal into its own typed error and renders
		// that; a raw envelope here would mean the message reached the operator
		// as an undecoded body instead of a sentence.
		if strings.Contains(result.Combined, `"code":"MERGE_QUEUE_OCCUPIED"`) {
			t.Fatalf("expected a decoded refusal, got the raw response body:\n%s", result.Combined)
		}
	})

	t.Run("merge_queue_override_advance_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"review", "queue", "override-advance", "--repository", reviewRepository, "--target-branch", "main", "--reason", "hotfix, reviewers unavailable", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "review/merge_queue_override_advance_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("merge_queue_override_advance_requires_reason", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		args := []string{"review", "queue", "override-advance", "--repository", reviewRepository, "--target-branch", "main", "--dry-run"}
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
			"review", "queue", "override-advance", "--repository", reviewRepository,
			"--target-branch", "main", "--reason", "hotfix, reviewers unavailable",
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
		args := []string{"review", "create", "--name", "json test", "--repository", reviewRepository,
			"--source-branch", "a", "--target-branch", "main", "--output", "json"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, `"name": "json test"`) {
			t.Fatalf("expected structured JSON result on stdout, got:\n%s", result.Combined)
		}
	})
}
