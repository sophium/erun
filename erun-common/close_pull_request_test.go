package eruncommon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The wire shape of the GitHub requests (list/comment/close) and GitHub's own
// failure response surfacing have no integration-suite equivalent, the same
// reasoning report_commit_status_test.go documents: validation, dry-run,
// remote-url parsing, and the no-token refusal are exercised end-to-end from
// the binary in erun-integration/exec_test.go instead.

// recordClosePullRequestComment and recordClosePullRequestClose capture
// method, path, auth header, and the one field each request body carries,
// isolated into their own helpers so the httptest handler composing the
// three call sites below stays under the module's cyclomatic complexity
// limit.
func recordClosePullRequestComment(dst *capturedRequest, r *http.Request) {
	dst.method, dst.path, dst.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
	var parsed struct {
		Body string `json:"body"`
	}
	_ = json.NewDecoder(r.Body).Decode(&parsed)
	dst.body.Description = parsed.Body
}

func recordClosePullRequestClose(dst *capturedRequest, r *http.Request) {
	dst.method, dst.path, dst.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
	var parsed struct {
		State string `json:"state"`
	}
	_ = json.NewDecoder(r.Body).Decode(&parsed)
	dst.body.State = parsed.State
}

func newClosePullRequestListCommentCloseServer(t *testing.T, gotComment, gotClose *capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/sophium/erun/pulls":
			if got, want := r.URL.RawQuery, "base=main&head=sophium%3Afeature%2Fadd-widget&state=open"; got != want {
				t.Errorf("unexpected list query: got %q, want %q", got, want)
			}
			_, _ = w.Write([]byte(`[{"number":42,"head":{"sha":"sourcesha123"}}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/sophium/erun/issues/42/comments":
			recordClosePullRequestComment(gotComment, r)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/sophium/erun/pulls/42":
			recordClosePullRequestClose(gotClose, r)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestClosePullRequestClosesAndCommentsWhenHeadMatches(t *testing.T) {
	var gotComment, gotClose capturedRequest
	server := newClosePullRequestListCommentCloseServer(t, &gotComment, &gotClose)
	defer server.Close()
	restoreBaseURL := githubAPIBaseURL
	githubAPIBaseURL = server.URL + "/"
	defer func() { githubAPIBaseURL = restoreBaseURL }()

	deps := ClosePullRequestDependencies{
		ResolveToken: func(string) (string, bool) { return "gho_test_token", true },
	}
	result, err := ClosePullRequest(Context{}, ClosePullRequestParams{
		RemoteURL:     "git@github.com:sophium/erun.git",
		Branch:        "feature/add-widget",
		TargetBranch:  "main",
		GatedCommit:   "sourcesha123",
		LandingCommit: "landedsha456",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantResult := ClosePullRequestResult{Owner: "sophium", Repo: "erun", Branch: "feature/add-widget", Found: true, Number: 42, Closed: true}
	if result != wantResult {
		t.Fatalf("unexpected result: got %+v, want %+v", result, wantResult)
	}
	if gotComment.method != http.MethodPost || gotComment.auth != "Bearer gho_test_token" {
		t.Fatalf("unexpected comment request: %+v", gotComment)
	}
	if !strings.Contains(gotComment.body.Description, "landedsha456") || !strings.Contains(gotComment.body.Description, "main") {
		t.Fatalf("expected comment to name the landing commit and target branch, got %q", gotComment.body.Description)
	}
	if gotClose.method != http.MethodPatch || gotClose.auth != "Bearer gho_test_token" || gotClose.body.State != "closed" {
		t.Fatalf("unexpected close request: %+v", gotClose)
	}
}

func TestClosePullRequestNoOpsWhenBranchHasNoOpenPullRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet {
			t.Fatalf("expected only the list call, got %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	restoreBaseURL := githubAPIBaseURL
	githubAPIBaseURL = server.URL + "/"
	defer func() { githubAPIBaseURL = restoreBaseURL }()

	deps := ClosePullRequestDependencies{
		ResolveToken: func(string) (string, bool) { return "gho_test_token", true },
	}
	result, err := ClosePullRequest(Context{}, ClosePullRequestParams{
		RemoteURL:     "https://github.com/sophium/erun.git",
		Branch:        "chore/no-pr",
		TargetBranch:  "main",
		GatedCommit:   "sourcesha123",
		LandingCommit: "landedsha456",
	}, deps)
	if err != nil {
		t.Fatalf("expected a no-op, got error: %v", err)
	}
	if result.Found || result.Closed {
		t.Fatalf("expected Found=false, Closed=false when there is no open pull request, got %+v", result)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one request (the list), got %d", calls)
	}
}

func TestClosePullRequestRefusesWhenHeadHasMoved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected the refusal before any mutating call, got %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"number":7,"head":{"sha":"movedsha999"}}]`))
	}))
	defer server.Close()
	restoreBaseURL := githubAPIBaseURL
	githubAPIBaseURL = server.URL + "/"
	defer func() { githubAPIBaseURL = restoreBaseURL }()

	deps := ClosePullRequestDependencies{
		ResolveToken: func(string) (string, bool) { return "gho_test_token", true },
	}
	_, err := ClosePullRequest(Context{}, ClosePullRequestParams{
		RemoteURL:     "https://github.com/sophium/erun.git",
		Branch:        "feature/add-widget",
		TargetBranch:  "main",
		GatedCommit:   "sourcesha123",
		LandingCommit: "landedsha456",
	}, deps)
	if err == nil {
		t.Fatal("expected a refusal when the pull request's head has moved past the gated commit")
	}
	var moved *ClosePullRequestHeadMovedError
	if !strings.Contains(err.Error(), "movedsha999") || !strings.Contains(err.Error(), "sourcesha123") {
		t.Fatalf("expected the refusal to name both shas, got: %v", err)
	}
	if e, ok := err.(*ClosePullRequestHeadMovedError); ok {
		moved = e
	} else {
		t.Fatalf("expected *ClosePullRequestHeadMovedError, got %T", err)
	}
	if moved.Number != 7 {
		t.Fatalf("expected the refusal to name pull request #7, got #%d", moved.Number)
	}
}

func TestClosePullRequestSurfacesAGitHubFailureResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	defer server.Close()
	restoreBaseURL := githubAPIBaseURL
	githubAPIBaseURL = server.URL + "/"
	defer func() { githubAPIBaseURL = restoreBaseURL }()

	deps := ClosePullRequestDependencies{
		ResolveToken: func(string) (string, bool) { return "gho_test_token", true },
	}
	_, err := ClosePullRequest(Context{}, ClosePullRequestParams{
		RemoteURL:     "https://github.com/sophium/erun.git",
		Branch:        "feature/add-widget",
		TargetBranch:  "main",
		GatedCommit:   "sourcesha123",
		LandingCommit: "landedsha456",
	}, deps)
	if err == nil {
		t.Fatal("expected a github failure response to surface as an error")
	}
	if !strings.Contains(err.Error(), "Resource not accessible") {
		t.Fatalf("expected github's own message in the error, got: %v", err)
	}
}
