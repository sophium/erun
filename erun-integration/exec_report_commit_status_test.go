package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testExecReportCommitStatusHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"exec", "report-commit-status", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_help", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusDryRunDefaultContext(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "success",
		"--description", "gate build passed",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_dry_run_default_context", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusDryRunExplicitContextAndTargetUrl(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "failure",
		"--description", "erun build failed against the prospective merge",
		"--remote-url", "git@github.com:sophium/erun.git",
		"--context", "erun/custom-gate",
		"--target-url", "https://example.com/build/123",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_dry_run_explicit_context_and_target_url", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusDryRunSshRemoteUrl(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "pending",
		"--description", "gate build running",
		"--remote-url", "ssh://git@github.com/sophium/erun.git",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_dry_run_ssh_remote_url", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusDryRunEmptyCommitTracesThenRefuses(t *testing.T) {
	// An empty string satisfies cobra.ExactArgs(1), so the shared validation must catch it.
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "",
		"--state", "success",
		"--description", "gate build passed",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an empty commit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_dry_run_empty_commit_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusDryRunInvalidStateTracesThenRefuses(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "groovy",
		"--description", "gate build passed",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an invalid state, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_dry_run_invalid_state_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusDryRunMissingDescriptionTracesThenRefuses(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "success",
		"--remote-url", "https://github.com/sophium/erun.git",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a missing description, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_dry_run_missing_description_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusDryRunMalformedRemoteUrlTracesThenRefuses(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "success",
		"--description", "gate build passed",
		"--remote-url", "https://gitlab.com/sophium/erun.git",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a non-github remote, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_dry_run_malformed_remote_url_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusDryRunMissingRemoteUrlTracesThenRefuses(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "success",
		"--description", "gate build passed",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a missing remote-url, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_dry_run_missing_remote_url_traces_then_refuses", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusRealRunFailsCleanlyWithoutAToken(t *testing.T) {
	// With no gh and no token, the real run must refuse before reaching the network.
	setup := env.New(t)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "success",
		"--description", "gate build passed",
		"--remote-url", "https://github.com/sophium/erun.git",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with no token available, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_real_run_fails_cleanly_without_a_token", normalize.Apply(result.Combined))
}

func testExecReportCommitStatusRealRunSucceeds(t *testing.T) {
	setup := env.New(t)
	github := githubCommitStatusStubServer(t, 0)
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "success",
		"--description", "gate build passed",
		"--remote-url", "https://github.com/sophium/erun.git",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_real_run_succeeds", normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
}

func testExecReportCommitStatusRealRunGithubRejects(t *testing.T) {
	setup := env.New(t)
	github := githubCommitStatusStubServer(t, http.StatusUnprocessableEntity)
	envVars := append(setup.Env(), githubStubEnv(github)...)
	result := erun.Run(t, []string{
		"exec", "report-commit-status", "deadbeefcafe",
		"--state", "success",
		"--description", "gate build passed",
		"--remote-url", "https://github.com/sophium/erun.git",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when github rejects the status, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "exec/report_commit_status_real_run_github_rejects", normalize.Apply(result.Combined, stubServerRule(github, "<GITHUB_API>")))
}

// githubCommitStatusStubServer refuses with forceStatus when it is non-zero.
func githubCommitStatusStubServer(t testing.TB, forceStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/sophium/erun/statuses/{commit}", func(w http.ResponseWriter, _ *http.Request) {
		if forceStatus != 0 {
			http.Error(w, `{"message":"Validation Failed"}`, forceStatus)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"state":"success"}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
