package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestDelete(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"delete", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_seeded_env", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev", "--dry-run"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "y\n",
		})
		golden.Equal(t, "delete/dry_run_with_seeded_env", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_remote_env_traces_namespace_delete", func(t *testing.T) {
		// Exercises delete.go on a remote environment: --dry-run must
		// trace the kubectl namespace delete command (with --ignore-not-found)
		// in addition to the local config rm trace, without touching the
		// cluster or prompting for confirmation.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"delete", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "delete/dry_run_with_remote_env_traces_namespace_delete", normalize.Apply(result.Combined))
	})

	t.Run("real_run_with_yes_flag_skips_confirmation_and_removes_config", func(t *testing.T) {
		// Exercises delete.go runDeleteCommand real-run path with --yes:
		// the confirmation prompt is bypassed, the env config tree is
		// physically removed, and the "deleted environment" line shows on
		// stdout. Stubs kubectl so the namespace delete succeeds without
		// touching a cluster.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envDir := filepath.Join(setup.ConfigHome, "erun", "team", "dev")
		if _, err := os.Stat(envDir); err != nil {
			t.Fatalf("seeded env config missing before delete: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"delete", "team", "dev", "--yes"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// Filesystem state — golden cannot assert this; keep the os.Stat
		// check.
		if _, err := os.Stat(envDir); !os.IsNotExist(err) {
			t.Errorf("expected env config tree to be removed at %s, stat err: %v", envDir, err)
		}
		golden.Equal(t, "delete/real_run_with_yes_flag_skips_confirmation_and_removes_config", normalize.Apply(result.Combined))
	})
}
