package review

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// The binary has no seam to point githubAPIBaseURL at httptest, so the GitHub
// wire shape is unit-tested here; the rest is in erun-integration/exec_test.go.

// capturedRequest is compared whole to stay under the cyclop limit.
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
	restoreBaseURL := registry.GithubAPIBaseURL
	registry.GithubAPIBaseURL = server.URL + "/"
	defer func() { registry.GithubAPIBaseURL = restoreBaseURL }()

	var resolvedOwner string
	deps := ReportCommitStatusDependencies{
		ResolveToken: func(owner string) (string, bool) {
			resolvedOwner = owner
			return "gho_test_token", true
		},
	}
	result, err := ReportCommitStatus(core.Context{}, ReportCommitStatusParams{
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
