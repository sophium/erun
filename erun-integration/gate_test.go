package integration

import (
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
}
