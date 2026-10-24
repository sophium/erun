package review

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// The matching-head close, the moved-head refusal, validation, dry-run and the
// no-token refusal are covered end-to-end in erun-integration/exec_close_pr_test.go;
// only a GitHub failure response is tested here.

func TestClosePullRequestSurfacesAGitHubFailureResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	defer server.Close()
	restoreBaseURL := registry.GithubAPIBaseURL
	registry.GithubAPIBaseURL = server.URL + "/"
	defer func() { registry.GithubAPIBaseURL = restoreBaseURL }()

	deps := ClosePullRequestDependencies{
		ResolveToken: func(string) (string, bool) { return "gho_test_token", true },
	}
	_, err := ClosePullRequest(core.Context{}, model.ClosePullRequestParams{
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
