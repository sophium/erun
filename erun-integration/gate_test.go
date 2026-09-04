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

// TestGate covers `erun gate list`/`erun gate show`: the queue
// view of gate runs, independent of whether an erun review exists for the
// change gated. Reporting a gate run's start and outcome is `erun exec
// gate-run`, covered in exec_test.go.
func TestGate(t *testing.T) {
	t.Parallel()
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"gate", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "gate/help", normalize.Apply(result.Combined))
	})

	t.Run("list_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"gate", "list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "gate/list_help", normalize.Apply(result.Combined))
	})

	t.Run("list_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"gate", "list", "--target-branch", "main", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "gate/list_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("list_dry_run_with_status_and_source_branch_filters", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{
			"gate", "list",
			"--target-branch", "main", "--source-branch", "feature/add-widget", "--status", "FAILED",
			"--dry-run",
		}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "gate/list_dry_run_with_status_and_source_branch_filters", normalize.Apply(result.Combined))
	})

	t.Run("show_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"gate", "show", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "gate/show_help", normalize.Apply(result.Combined))
	})

	t.Run("show_dry_run", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"gate", "show", "gate-run-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "gate/show_dry_run", normalize.Apply(result.Combined))
	})

	// erun#2052: /v1/gate-runs merged and closed its issue while every
	// deployed control plane still predated it, and every real caller of
	// `erun gate list` saw only an opaque "http 404: 404 page not found" with
	// nothing distinguishing "the plane's router has never heard of this
	// path" from an ordinary application-level not-found. This stub registers
	// no routes at all, so it answers Go's own default 404 body -- exactly
	// what an undeployed route looks like on a real plane -- and locks in
	// that the CLI now names the actual cause and the two commands that
	// confirm it, instead of leaving the operator to rediscover both by hand.
	t.Run("real_run_route_not_registered_on_plane_reports_deploy_gap_hint", func(t *testing.T) {
		setup := env.New(t)
		platform := httptest.NewServer(http.NewServeMux())
		t.Cleanup(platform.Close)
		platformAlias(t, setup, platform)
		result := erun.Run(t, []string{"gate", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a 404 from an unregistered route, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "gate/real_run_route_not_registered_on_plane_reports_deploy_gap_hint",
			normalize.Apply(result.Combined, stubServerRule(platform, "<PLATFORM_API>")))
	})
}
