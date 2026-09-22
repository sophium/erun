package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// gateRunListAPIStubServer answers `erun gate list`'s own read with runs, so a
// scenario exercises the real listing path end to end rather than only the
// --dry-run trace branch. An empty (non-nil) slice is a real "no gate runs".
func gateRunListAPIStubServer(t testing.TB, runs []map[string]any) *httptest.Server {
	t.Helper()
	if runs == nil {
		runs = []map[string]any{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/gate-runs", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(runs)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

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

	t.Run("list_unknown_status_is_refused_as_a_bad_argument", func(t *testing.T) {
		// A mistyped --status must fail as a bad argument naming the accepted
		// values. Passing it through to the platform filter would come back as
		// an empty listing -- "no gate runs" and exit 0 -- which is
		// indistinguishable from a real empty result on the merge queue's
		// audit trail.
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"gate", "list", "--status", "bogus"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("exit 0 for an unrecognised --status, want a non-zero exit:\n%s", result.Combined)
		}
		golden.Equal(t, "gate/list_unknown_status_is_refused_as_a_bad_argument", normalize.Apply(result.Combined))
	})

	t.Run("list_with_a_valid_status_and_no_matches_is_an_empty_result", func(t *testing.T) {
		// The other direction: a valid --status that matches nothing is a real
		// empty result -- exit 0 and "no gate runs" -- and the normalized
		// filter is what reached the platform.
		setup := env.New(t)
		server := gateRunListAPIStubServer(t, nil)
		platformAlias(t, setup, server)
		result := erun.Run(t, []string{"gate", "list", "--status", "FAILED"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d for a valid --status with no matches, want 0:\n%s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "gate/list_with_a_valid_status_and_no_matches_is_an_empty_result",
			normalize.Apply(result.Combined, stubServerRule(server, "<PLATFORM_API>")))
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

	// /v1/gate-runs merged and closed its issue while every
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
