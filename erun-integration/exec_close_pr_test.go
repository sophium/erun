package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testExecClosePrHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "close-pr", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/close_pr_help", normalize.Apply(result.Combined))
}

func testExecClosePrDryRunTracesLookup(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "close-pr", "feature/add-widget",
		"--target", "main",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--gated-commit", "sourcesha0000000000000000000000000000000",
		"--landing-commit", "landedsha0000000000000000000000000000000",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/close_pr_dry_run_traces_lookup", normalize.Apply(result.Combined))
}

func testExecClosePrDryRunMissingRequiredFlagsTracesThenRefuses(t *testing.T) {
	// All four flags are validated as one refusal, so one scenario covers any subset.
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "close-pr", "feature/add-widget", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for missing required flags, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/close_pr_dry_run_missing_required_flags_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecClosePrDryRunEmptyBranchTracesThenRefuses(t *testing.T) {
	// An empty BRANCH satisfies cobra.ExactArgs(1); the shared validation catches it.
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "close-pr", "",
		"--target", "main",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--gated-commit", "sourcesha0000000000000000000000000000000",
		"--landing-commit", "landedsha0000000000000000000000000000000",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an empty branch, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/close_pr_dry_run_empty_branch_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecClosePrDryRunMalformedRemoteUrlTracesThenRefuses(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "close-pr", "feature/add-widget",
		"--target", "main",
		"--remote-url", "https://gitlab.com/sophium/erun.git",
		"--gated-commit", "sourcesha0000000000000000000000000000000",
		"--landing-commit", "landedsha0000000000000000000000000000000",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a non-github remote, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/close_pr_dry_run_malformed_remote_url_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecClosePrRealRunFailsCleanlyWithoutAToken(t *testing.T) {
	// With no gh and no token, the refusal comes before any network call.
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "close-pr", "feature/add-widget",
		"--target", "main",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--gated-commit", "sourcesha0000000000000000000000000000000",
		"--landing-commit", "landedsha0000000000000000000000000000000",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with no token available, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/close_pr_real_run_fails_cleanly_without_a_token", normalize.Apply(result.Combined))
}

func testExecClosePrRealRunNoOpenPullRequestIsANoOp(t *testing.T) {
	// A queued branch with no open pull request is a no-op, not an error.
	setup := env.New(t)
	github := githubPullRequestStubServer(t, nil)
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "close-pr", "feature/add-widget",
		"--target", "main",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--gated-commit", "sourcesha0000000000000000000000000000000",
		"--landing-commit", "landedsha0000000000000000000000000000000",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/close_pr_real_run_no_open_pull_request_is_a_no_op", normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
}

func testExecClosePrRealRunCommentsAndClosesAMatchingPullRequest(t *testing.T) {
	setup := env.New(t)
	github := githubPullRequestStubServer(t, &githubPullRequestStubPull{
		Number: 42, HeadSHA: "sourcesha0000000000000000000000000000000",
	})
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "close-pr", "feature/add-widget",
		"--target", "main",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--gated-commit", "sourcesha0000000000000000000000000000000",
		"--landing-commit", "landedsha0000000000000000000000000000000",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/close_pr_real_run_comments_and_closes_a_matching_pull_request", normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
}

func testExecClosePrRealRunRefusesWhenHeadMoved(t *testing.T) {
	// A moved head means closing would discard what moved it.
	setup := env.New(t)
	github := githubPullRequestStubServer(t, &githubPullRequestStubPull{
		Number: 42, HeadSHA: "movedsha00000000000000000000000000000000",
	})
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "close-pr", "feature/add-widget",
		"--target", "main",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--gated-commit", "sourcesha0000000000000000000000000000000",
		"--landing-commit", "landedsha0000000000000000000000000000000",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the pull request head moved, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/close_pr_real_run_refuses_when_head_moved", normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
}

// githubPullRequestStubPull is the open pull request to find, or nil for none.
type githubPullRequestStubPull struct {
	Number  int
	HeadSHA string
}

func githubPullRequestStubServer(t testing.TB, pull *githubPullRequestStubPull) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/sophium/erun/pulls", func(w http.ResponseWriter, _ *http.Request) {
		if pull == nil {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = fmt.Fprintf(w, `[{"number":%d,"head":{"sha":%q}}]`, pull.Number, pull.HeadSHA)
	})
	mux.HandleFunc("POST /repos/sophium/erun/issues/{number}/comments", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	mux.HandleFunc("PATCH /repos/sophium/erun/pulls/{number}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"state":"closed"}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
