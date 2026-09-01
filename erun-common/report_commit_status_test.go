package eruncommon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The wire shape of the GitHub request (method, path, auth header, body) and
// GitHub's own failure response surfacing have no integration-suite
// equivalent: the compiled `erun` binary has no seam to point githubAPIBaseURL
// at a local httptest server the way the chart-registry probe does, so these
// two stay as erun-common unit tests. Everything else (validation, dry-run,
// remote-url parsing, the no-token refusal) is exercised end-to-end from the
// binary in erun-integration/exec_test.go.

// capturedRequest is compared with == against a whole expected value below,
// rather than field-by-field, to keep this test's cyclomatic complexity under
// the module's cyclop limit -- one struct comparison instead of five branches.
type capturedRequest struct {
	method, path, auth string
	body               commitStatusRequestBody
}

func TestReportCommitStatusPostsToGitHubWithTheRightShape(t *testing.T) {
	var got capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got.body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	restoreBaseURL := githubAPIBaseURL
	githubAPIBaseURL = server.URL + "/"
	defer func() { githubAPIBaseURL = restoreBaseURL }()

	var resolvedOwner string
	deps := ReportCommitStatusDependencies{
		ResolveToken: func(owner string) (string, bool) {
			resolvedOwner = owner
			return "gho_test_token", true
		},
	}
	result, err := ReportCommitStatus(Context{}, ReportCommitStatusParams{
		RemoteURL:   "git@github.com:sophium/erun.git",
		Commit:      "abc123def456",
		State:       CommitStatusSuccess,
		Description: "gate build passed",
		Context:     "erun/merge-gate",
		TargetURL:   "https://example.com/log",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := capturedRequest{
		method: http.MethodPost,
		path:   "/repos/sophium/erun/statuses/abc123def456",
		auth:   "Bearer gho_test_token",
		body: commitStatusRequestBody{
			State:       "success",
			Context:     "erun/merge-gate",
			Description: "gate build passed",
			TargetURL:   "https://example.com/log",
		},
	}
	if got != want {
		t.Fatalf("unexpected request: got %+v, want %+v", got, want)
	}
	if resolvedOwner != "sophium" {
		t.Fatalf("expected token resolution scoped to owner sophium, got %s", resolvedOwner)
	}
	wantResult := ReportCommitStatusResult{Owner: "sophium", Repo: "erun", Commit: "abc123def456", State: "success", Context: "erun/merge-gate"}
	if result != wantResult {
		t.Fatalf("unexpected result: got %+v, want %+v", result, wantResult)
	}
}

func TestReportCommitStatusSurfacesAGitHubFailureResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	defer server.Close()
	restoreBaseURL := githubAPIBaseURL
	githubAPIBaseURL = server.URL + "/"
	defer func() { githubAPIBaseURL = restoreBaseURL }()

	deps := ReportCommitStatusDependencies{
		ResolveToken: func(string) (string, bool) { return "gho_test_token", true },
	}
	_, err := ReportCommitStatus(Context{}, ReportCommitStatusParams{
		RemoteURL:   "https://github.com/sophium/erun.git",
		Commit:      "abc123",
		State:       CommitStatusFailure,
		Description: "erun build failed",
	}, deps)
	if err == nil {
		t.Fatal("expected a github failure response to surface as an error")
	}
	if !strings.Contains(err.Error(), "Resource not accessible") {
		t.Fatalf("expected github's own message in the error, got: %v", err)
	}
}
