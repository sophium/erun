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
	// branch, target, gated-commit, and landing-commit are all validated
	// together as one refusal, so omitting any subset produces the same
	// message -- one scenario (omitting every flag) covers the branch.
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "close-pr", "feature/add-widget", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for missing required flags, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/close_pr_dry_run_missing_required_flags_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecClosePrDryRunEmptyBranchTracesThenRefuses(t *testing.T) {
	// BRANCH is a positional arg an empty string can still satisfy
	// cobra.ExactArgs(1), so the shared validation (and its trace) is what
	// actually catches this, not command-tree wiring.
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
	// A non-github.com remote reaches parseGitHubOwnerRepo's rejection
	// branch, distinct from the missing-remote-url case above.
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
	// No --dry-run, no gh on the scrubbed PATH, and no GITHUB_TOKEN/GH_TOKEN
	// in the environment: the real-run token resolution must refuse before
	// ever reaching the network, naming the fix rather than a raw HTTP error.
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
	// findAndClosePullRequest's no-pulls branch: a queued plain branch
	// with no open pull request is legitimate, so this is a no-op result
	// rather than an error.
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
	// The full mutating path: postGitHubIssueComment then
	// closeGitHubPullRequest, once the open pull request's head matches
	// --gated-commit.
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
	// The open pull request's head no longer matches --gated-commit --
	// something pushed to the branch after the gate fetched it, so
	// closing would silently discard whatever moved it.
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

// githubPullRequestStubPull is the one open pull request `exec close-pr`
// should find for feature/add-widget -> main, or nil for the no-open-PR
// scenario.
type githubPullRequestStubPull struct {
	Number  int
	HeadSHA string
}

// githubPullRequestStubServer answers the three calls `exec close-pr` makes
// in real-run mode: the open-pull-requests lookup, the landing-commit
// comment, and the close itself.
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
